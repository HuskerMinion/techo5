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
	"fmt"
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
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
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

// Message is an announcement as it arrives: who it came from, what was said, and the saying of it.
//
// Audio is the point and text is the trimming. A device with a screen shows the words when there are
// any, and every device plays the voice — which is what makes this work on a Dot, where there is
// nothing to read. An announcement from an automation has text and no voice; one spoken into a
// device has voice and, when Home Assistant heard it too, text as well.
type Message struct {
	From string `json:"from"`
	Text string `json:"text"`

	// Voice is what was said, at the microphone's rate, empty for an announcement nobody spoke.
	Voice []int16 `json:"-"`
}

type Feature struct {
	action *esphome.Action

	mu    sync.Mutex
	last  Message
	until time.Time

	// recording is whether this device has its microphone open for an announcement now, for the
	// screen and the ring to say so: a microphone that is open and does not look it is the thing
	// people mind most.
	recording bool

	// Changed fires when something arrives or stops showing, or when this device starts or stops
	// recording one, so the screen redraws and the ring follows.
	Changed hook.Hook[struct{}]

	// Arrived fires once for each announcement taken, carrying it, for anything that wants the event
	// rather than the state: the ring's flash, and anything later that wants to keep them.
	Arrived hook.Hook[Message]
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
			// With words, it says them: an automation telling the house the washing is done. With
			// none, it opens the microphone and sends whoever is standing here, which is what a
			// person means by announcing and what an intent for "announce" should call.
			Run: func(c esphome.Call) (any, error) {
				if text := strings.TrimSpace(c.String("text")); text != "" {
					safe.Go("announce", func() { shared.Say(text) })
					return nil, nil
				}
				safe.Go("announce", func() { shared.Speak(context.Background()) })
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
	m, err := decode(r)
	if err != nil {
		http.Error(w, "that was not an announcement", http.StatusBadRequest)
		return
	}
	m.From, m.Text = clip(m.From, 40), clip(m.Text, maxText)
	if m.Text == "" && len(m.Voice) == 0 {
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

	f.Arrived.Emit(m)

	quiet := config.Quiet()
	slog.Info("announcement", "from", m.From, "words", m.Text != "", "seconds", seconds(m.Voice), "quiet", quiet)
	f.Changed.Emit(struct{}{})

	// In quiet hours it is shown and not heard. The screen holds it for its forty-five seconds either
	// way, which is how somebody walking past at midnight finds out what they missed.
	if quiet {
		return
	}
	safe.Go("announcement", func() { f.sound(m) })
}

// sound is the chime and then the voice, under one claim on the speaker so that stopping an
// announcement stops all of it, and so that whatever was playing is put back afterwards.
func (f *Feature) sound(m Message) {
	claim := speaker.Sound().Claim("announcement", func(ctx context.Context, p *speaker.Player) error {
		p.Chime(chimeLevel, announceTone...)
		if len(m.Voice) == 0 {
			return nil
		}
		// The chime is still leaving the driver, and the two running together would be a chord.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(chimeTail):
		}
		p.PlayVoice(m.Voice)
		return nil
	})
	<-claim.Done()
	if err := claim.Err(); err != nil {
		slog.Warn("playing an announcement failed", "from", m.From, "err", err)
	}
}

// chimeTail is the gap between the chime and the voice: enough for one to finish, short enough that
// the two are heard as one event.
const chimeTail = 400 * time.Millisecond

// seconds is how long a clip runs, for the log.
func seconds(voice []int16) float64 {
	if len(voice) == 0 {
		return 0
	}
	return float64(len(voice)) / float64(mic.Rate)
}

// Say tells every other device in the house, and shows it here as well so the room it was sent from
// can see that it went. Text with no voice is an automation talking; the screens show it and the
// Dots chime and say nothing, which is the honest result of sending words to a device that cannot
// read them aloud.
func (f *Feature) Say(text string) { f.send(Message{Text: clip(strings.TrimSpace(text), maxText)}) }

// Speak records whoever is standing at this device and sends that, which is the announcement people
// mean: a voice in every room, with nothing typed and nothing understood by anybody in between. A
// recording nobody spoke into is dropped rather than sent as a chime and silence.
func (f *Feature) Speak(ctx context.Context) {
	if config.Get().Home.HouseWord == "" {
		slog.Warn("nothing announced: this house has no word set, so nobody would take it")
		return
	}

	f.mu.Lock()
	busy := f.recording
	f.recording = true
	f.mu.Unlock()
	if busy {
		slog.Info("already recording an announcement")
		return
	}
	defer func() {
		f.mu.Lock()
		f.recording = false
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
	}()
	f.Changed.Emit(struct{}{})

	voice := record(ctx)
	speaker.Sound().Interject(func(p *speaker.Player) { p.Chime(promptLevel, confirm...) })
	if len(voice) == 0 {
		slog.Info("nothing was said, so nothing was announced")
		return
	}
	f.send(Message{Voice: voice})
}

// Recording is whether this device is taking an announcement now, for the screen and the ring to say
// so: a microphone that is open and does not look it is the thing people mind most.
func (f *Feature) Recording() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.recording
}

// send pushes one to every peer at once and shows it here. Every device is told in parallel, so the
// house hears it together rather than room by room down a list.
func (f *Feature) send(m Message) {
	if m.Text == "" && len(m.Voice) == 0 {
		return
	}
	word := config.Get().Home.HouseWord
	if word == "" {
		slog.Warn("nothing announced: this house has no word set, so nobody would take it")
		return
	}
	m.From = config.Get().Device.Name
	body, headers := encode(m)

	peers := Peers()
	slog.Info("announcing", "text", m.Text, "seconds", seconds(m.Voice), "to", len(peers))
	var wg sync.WaitGroup
	for _, p := range peers {
		wg.Add(1)
		go func(p Peer) {
			defer wg.Done()
			if err := post(p, word, body, headers); err != nil {
				slog.Warn("announcement not delivered", "to", p.Name, "err", err)
			}
		}(p)
	}
	wg.Wait()
	f.show(m)
}

func post(p Peer, word string, body []byte, headers map[string]string) error {
	ctx, cancel := context.WithTimeout(context.Background(), sendWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://%s/announce", net.JoinHostPort(p.Address, fmt.Sprint(p.Port))), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set(header, word)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
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
