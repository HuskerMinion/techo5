// Package streaming makes the device a speaker other apps play to: AirPlay, from an iPhone, iPad or
// Mac (shairport-sync), and Spotify Connect, from the Spotify app (librespot). Each is a switch, off on
// a new device, in Home Assistant, on the settings screen and on the setup page.
//
// Both programs are found on the network by avahi, which runs only while one of them is on; the
// daemon's own announcements (ESPHome, Sendspin) go on beside it. Each program hands the daemon raw
// audio on its standard output, which plays as a received track, as a phone over Bluetooth does: a
// voice turn ducks or pauses it, an alarm sounds over it, and whatever is started last has the
// speaker. Each is restarted if it stops, after a pause that grows while it keeps stopping.
//
// A pause, a play and a skip on the device reach an AirPlay sender over DACP, its remote control
// (dacp.go), so the phone pauses with the speaker and the track stays paused on the screen: until half
// an hour after the phone ends its session, if it does not play again (airplayKeptFor). A pause on the
// phone shows there too. Spotify Connect has no such way back, nor has an AirPlay sender whose session
// names none: a pause on the device stops its track, and the phone plays on to a speaker that drops
// what it sends until it goes quiet.
//
// AirPlay has been tried with an iPhone on a Show: playing, pausing and skipping from either end, and
// stopping. Spotify Connect has been tried on a Show as well.
package streaming

import (
	"context"
	"log/slog"
	"os"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	if Here {
		component.Register(component.Network, Get(), component.Order(80))
		media.ReceiverControl(home.AirPlayName, airplayRemote.control)
		media.StopOnPause(home.SpotifyName)
	}
}

// Where the programs are in the image (tools/linux/packages-rootfs.txt, build-librespot.sh).
var (
	avahiPath     = "/usr/sbin/avahi-daemon"
	shairportPath = "/usr/bin/shairport-sync"
	librespotPath = "/usr/local/bin/techo5-librespot"
)

// runDir holds what the programs are given: their configuration, and the pipes for what they say.
const runDir = "/run/techo5-streaming"

type Feature struct {
	airplay, spotify *esphome.Switch

	mu      sync.Mutex
	running context.CancelFunc // what runs for the switches as they were last settled
	done    chan struct{}      // closed once what running stops has stopped
	wanted  [2]bool            // AirPlay, Spotify, as last settled
	wake    chan struct{}

	// Changed fires when a switch changes; listeners must not block.
	Changed hook.Hook[struct{}]
}

var shared = build()

func Get() *Feature { return shared }

func build() *Feature {
	f := &Feature{wake: make(chan struct{}, 1)}
	f.airplay = &esphome.Switch{
		Base:      esphome.Base{ObjectID: "airplay", Name: "AirPlay", Icon: "mdi:apple-airplay", Category: esphome.CategoryConfig},
		OnCommand: f.SetAirPlay,
	}
	f.spotify = &esphome.Switch{
		Base:      esphome.Base{ObjectID: "spotify_connect", Name: "Spotify Connect", Icon: "mdi:spotify", Category: esphome.CategoryConfig},
		OnCommand: f.SetSpotify,
	}
	return f
}

func (f *Feature) Name() string { return "streaming" }

func (f *Feature) Entities() []esphome.Entity { return []esphome.Entity{f.airplay, f.spotify} }

func (f *Feature) Restore(c config.Config) {
	f.airplay.Set(c.Streaming.AirPlay)
	f.spotify.Set(c.Streaming.Spotify)
}

// SetAirPlay and SetSpotify are the switches, from Home Assistant, the screen or the setup page.
func (f *Feature) SetAirPlay(on bool) { f.set(f.airplay, on, config.Set().Streaming().AirPlay) }
func (f *Feature) SetSpotify(on bool) { f.set(f.spotify, on, config.Set().Streaming().Spotify) }

func (f *Feature) set(s *esphome.Switch, on bool, save func(bool) error) {
	if err := save(on); err != nil {
		slog.Error("saving a setting failed", "setting", s.ObjectID, "err", err)
		s.Set(!on)
		return
	}
	s.Set(on)
	slog.Info("setting changed", "setting", s.ObjectID, "using", on)
	f.Changed.Emit(struct{}{})
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// Installed is whether this image carries what the switches start: an older image, or one built
// without them, offers the switches but cannot keep them.
func Installed() (airplay, spotify bool) {
	have := func(p string) bool { _, err := os.Stat(p); return err == nil }
	return have(avahiPath) && have(shairportPath), have(avahiPath) && have(librespotPath)
}

// Run keeps what runs matching the switches.
func (f *Feature) Run(ctx context.Context) error {
	defer f.stop()
	for {
		f.settle(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-f.wake:
		}
	}
}

// settle starts or stops the receivers, and avahi with them, to match the switches. Any change stops
// everything and starts again what is wanted: the switches are seldom touched, and one start keeps the
// order (avahi first) simple.
func (f *Feature) settle(parent context.Context) {
	c := config.Get().Streaming
	want := [2]bool{c.AirPlay, c.Spotify}
	f.mu.Lock()
	same := want == f.wanted && (f.running != nil || (!want[0] && !want[1]))
	f.mu.Unlock()
	if same {
		return
	}
	f.stop()
	if !want[1] {
		forgetSpotify()
	}
	if !want[0] && !want[1] {
		f.mu.Lock()
		f.wanted = want
		f.mu.Unlock()
		slog.Info("streaming: off")
		return
	}
	haveAirPlay, haveSpotify := Installed()
	if want[0] && !haveAirPlay {
		slog.Error("streaming: AirPlay is on, but this image does not carry shairport-sync and avahi")
	}
	if want[1] && !haveSpotify {
		slog.Error("streaming: Spotify Connect is on, but this image does not carry librespot and avahi")
	}
	runAirPlay, runSpotify := want[0] && haveAirPlay, want[1] && haveSpotify
	ctx, cancel := context.WithCancel(parent)
	f.mu.Lock()
	f.running, f.wanted = cancel, want
	f.mu.Unlock()
	if !runAirPlay && !runSpotify {
		return
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		slog.Error("streaming: making its run folder failed", "err", err)
		return
	}
	name := config.Get().Device.Name
	done := make(chan struct{})
	f.mu.Lock()
	f.done = done
	f.mu.Unlock()
	safe.Go("streaming", func() {
		defer close(done)
		runGroup(ctx, name, runAirPlay, runSpotify)
	})
	slog.Info("streaming: on", "airplay", runAirPlay, "spotify", runSpotify, "name", name)
}

// stop ends whatever runs and waits for it to be gone, so a new avahi never starts beside one still
// on its way out.
func (f *Feature) stop() {
	f.mu.Lock()
	cancel, done := f.running, f.done
	f.running, f.done = nil, nil
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
