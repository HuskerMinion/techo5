package media

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
	"github.com/HuskerMinion/techo5/echod/internal/lib/radiobrowser"
)

// A stream the device cannot decode itself (AAC, HLS and the rest: much of the radio there is) is
// handed to the music library where one is set up (config.MusicAssistant). The library converts it
// and plays it here over Sendspin, as it plays its own music, so a station plays whatever its format,
// from wherever it was started: a voice request, the radio page, the setup page.

// libraryWait is how long the library has to get a stream playing here.
var libraryWait = 20 * time.Second

// libraryPlayer is this device's player id at the library, its factory MAC; a variable for the tests.
var libraryPlayer = layout.FactoryMAC

// errNoLibrary is a device with no music library set up to hand a stream to.
var errNoLibrary = errors.New("no music library is set up on this device")

// errOvertaken is a hand-off that something played or stopped since has made unwanted.
var errOvertaken = musicassistant.ErrOvertaken

// viaLibrary has the music library play url here, and waits for it to be playing, while wanted
// says it still is (nil for always).
func viaLibrary(url string, wanted func() bool) error {
	m := config.Get().MusicAssistant
	if !m.Set() {
		return errNoLibrary
	}
	// The library fetches what it is handed, from where it is, which can be another network than this
	// device's: only an address on the internet goes to it, never one into a home.
	rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
	public := radiobrowser.Resolves(rctx, url)
	rcancel()
	if !public {
		return errors.New("its address is not a public one, so it is not handed to the music library")
	}
	if wanted != nil && !wanted() {
		return errOvertaken
	}
	player, err := libraryPlayer()
	if err != nil || player == "" {
		return errors.New("this device does not know its own player id")
	}
	slog.Info("media: handing a stream this device cannot decode to the music library", "url", url)
	ctx, cancel := context.WithTimeout(context.Background(), libraryWait+20*time.Second)
	defer cancel()
	c := musicassistant.Client{URL: m.URL, Token: m.Token}
	return c.PlayChecked(ctx, player, url, libraryWait, time.Second, wanted)
}

// libraryState caches LibraryReady's answer: it is asked for every search and list.
var libraryState struct {
	sync.Mutex
	ready bool
	at    time.Time
	of    config.MusicAssistant
}

// libraryFresh is how long that answer is trusted.
const libraryFresh = 30 * time.Second

// LibraryReady is whether the music library is set up and this device is connected to it as a player
// right now: what it takes for a stream the device cannot decode to play anyway. A library that is set
// up but unreachable (its VPN down, the server off) is as good as none, and what is offered then is
// only what the device plays itself.
func LibraryReady() bool {
	m := config.Get().MusicAssistant
	if !m.Set() {
		return false
	}
	libraryState.Lock()
	if libraryState.of == m && time.Since(libraryState.at) < libraryFresh {
		ready := libraryState.ready
		libraryState.Unlock()
		return ready
	}
	libraryState.Unlock()
	ready := false
	if player, err := libraryPlayer(); err == nil && player != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		p, err := musicassistant.Client{URL: m.URL, Token: m.Token}.Player(ctx, player)
		cancel()
		ready = err == nil && p.Available
	}
	libraryState.Lock()
	libraryState.ready, libraryState.at, libraryState.of = ready, time.Now(), m
	libraryState.Unlock()
	return ready
}

// handOff is Stream.handOff: a url Play started that the device could not decode, handed to the music
// library. With no library set up, OnUnplayable says so, unless something else was asked for since.
func (p *Player) handOff(url string, wanted func() bool) {
	err := viaLibrary(url, wanted)
	switch {
	case errors.Is(err, errNoLibrary):
		if wanted() {
			p.OnUnplayable.Emit(url)
		}
	case err != nil && !errors.Is(err, errOvertaken):
		slog.Warn("media: the music library did not play it either", "url", url, "err", err)
	}
}
