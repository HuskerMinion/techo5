// Package announce says something in every room: one device tells the others, they chime and put it
// on their screens.
//
// It needs no Home Assistant and no internet — the devices find each other over mDNS and talk to each
// other directly — which is the point of it. A house with the server down can still call everybody to
// dinner.
//
// **Trust.** A device that plays whatever it is sent is a device anything on the network can make
// talk. So an announcement carries a word that the house shares, set on each device's setup page,
// and a device with no word set refuses everything: nothing works until somebody decides it should,
// which is the right way round for a thing that makes noise in a bedroom.
//
// **Quiet hours** are respected: inside them an announcement is shown and not sounded. Alarms,
// timers and calls are not announcements and are not affected (config/quiet.go).
package announce

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	component.Register(component.Network, Get(), component.Order(74))
}

const (
	// shows is how long an announcement stays on the screen.
	shows = 45 * time.Second

	// sendWait bounds telling one device, so a device that is off does not hold up the rest.
	sendWait = 4 * time.Second

	// maxText is as much as an announcement carries: a line, not a letter.
	maxText = 160

	// header is where the house word goes.
	header = "X-Techo5-House"

	// chimeLevel is loud enough to fetch somebody from the next chair, not from another room: an
	// announcement is about to say something, and the words are the point.
	chimeLevel = 0.4
)

// announceTone is what an announcement arrives with: two notes rising, distinct from the alarm's.
var announceTone = []speaker.Note{{Freq: 659, Ms: 90}, {Freq: 988, Ms: 160}}

// Message is an announcement as it arrives.
type Message struct {
	From string `json:"from"`
	Text string `json:"text"`
}

type Feature struct {
	action *esphome.Action

	mu    sync.Mutex
	last  Message
	until time.Time

	// Changed fires when something arrives or stops showing, so the screen redraws.
	Changed hook.Hook[struct{}]
}

var (
	once   sync.Once
	shared *Feature
)

func Get() *Feature {
	once.Do(func() {
		shared = &Feature{}
		shared.action = &esphome.Action{
			Name: "announce_house",
			Args: []esphome.Arg{{Name: "text", Type: esphome.ArgString}},
			Run: func(c esphome.Call) (any, error) {
				safe.Go("announce", func() { shared.Say(c.String("text")) })
				return nil, nil
			},
		}
		web.Handle("/announce", "", announceOpen, shared.receive)
	})
	return shared
}

func (f *Feature) Name() string { return "announcements" }

func (f *Feature) Actions() []*esphome.Action { return []*esphome.Action{f.action} }

// announceOpen is whether this device takes announcements at all: only once the house has a word.
func announceOpen() bool { return config.Get().Home.HouseWord != "" }

// Showing is what is on the screen now, if anything.
func (f *Feature) Showing() (Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Now().After(f.until) {
		return Message{}, false
	}
	return f.last, true
}

// receive takes an announcement from another device.
func (f *Feature) receive(w http.ResponseWriter, r *http.Request) {
	word := config.Get().Home.HouseWord
	if word == "" || r.Header.Get(header) != word {
		slog.Warn("announcement refused: the house word did not match", "from", r.RemoteAddr)
		http.Error(w, "not this house", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post an announcement", http.StatusMethodNotAllowed)
		return
	}
	var m Message
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&m); err != nil {
		http.Error(w, "that was not an announcement", http.StatusBadRequest)
		return
	}
	m.From, m.Text = clip(m.From, 40), clip(m.Text, maxText)
	if m.Text == "" {
		http.Error(w, "an announcement says something", http.StatusBadRequest)
		return
	}
	f.show(m)
	w.WriteHeader(http.StatusNoContent)
}

// show puts an announcement on the screen and chimes for it, unless the house is meant to be quiet,
// in which case it is shown and not heard.
func (f *Feature) show(m Message) {
	f.mu.Lock()
	f.last, f.until = m, time.Now().Add(shows)
	f.mu.Unlock()

	quiet := config.Quiet()
	slog.Info("announcement", "from", m.From, "quiet", quiet)
	if !quiet {
		speaker.Sound().Interject(func(p *speaker.Player) { p.Chime(chimeLevel, announceTone...) })
	}
	f.Changed.Emit(struct{}{})
}

// Say tells every other device in the house, and shows it here as well so the room it was sent from
// can see that it went.
func (f *Feature) Say(text string) {
	text = clip(strings.TrimSpace(text), maxText)
	if text == "" {
		return
	}
	word := config.Get().Home.HouseWord
	if word == "" {
		slog.Warn("nothing announced: this house has no word set, so nobody would take it")
		return
	}
	me := config.Get().Device.Name
	body, err := json.Marshal(Message{From: me, Text: text})
	if err != nil {
		return
	}

	peers := Peers()
	slog.Info("announcing", "text", text, "to", len(peers))
	var wg sync.WaitGroup
	for _, p := range peers {
		wg.Add(1)
		go func(p Peer) {
			defer wg.Done()
			if err := post(p, word, body); err != nil {
				slog.Warn("announcement not delivered", "to", p.Name, "err", err)
			}
		}(p)
	}
	wg.Wait()
	f.show(Message{From: me, Text: text})
}

func post(p Peer, word string, body []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), sendWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://%s/announce", net.JoinHostPort(p.Address, fmt.Sprint(p.Port))), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set(header, word)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s said %s", p.Name, resp.Status)
	}
	return nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
