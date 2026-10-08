//go:build !dot && !spot

package deck

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/obsws"
)

func init() {
	component.Register(component.Device, Get(), component.Order(38))
}

// pressTimeout bounds one press: OBS answers in milliseconds, or is not there.
const pressTimeout = 5 * time.Second

// lookEvery is how often the settings are compared with what the connection was made with, so a
// change from the setup page or a restore is picked up without anybody calling Reload.
const lookEvery = 5 * time.Second

// Feature is the deck.
type Feature struct {
	// Changed fires when there is something new to draw: OBS's state, or the connection coming and
	// going. Listeners must not block.
	Changed hook.Hook[struct{}]

	obs *obsws.Client
	pcs computers

	mu      sync.Mutex
	conf    obsws.Config
	tracked []obsws.Item
	reload  chan struct{}
	check   chan struct{}
}

var (
	once   sync.Once
	shared *Feature
)

// Get is the deck.
func Get() *Feature {
	once.Do(func() {
		f := &Feature{reload: make(chan struct{}, 1), check: make(chan struct{}, 1)}
		f.obs = obsws.New(func() { f.Changed.Emit(struct{}{}) })
		shared = f
	})
	return shared
}

func (f *Feature) Name() string { return "deck" }

// Run keeps the connection to OBS for as long as one is set up, and follows changes to it.
func (f *Feature) Run(ctx context.Context) error {
	f.follow()
	go f.obs.Run(ctx, f.obsConfig)
	go f.watchComputers(ctx)
	t := time.NewTicker(lookEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-f.reload:
			f.recheck()
		}
		f.follow()
	}
}

// watchComputers asks the paired computers who they are now and then, and whenever asked to.
func (f *Feature) watchComputers(ctx context.Context) {
	t := time.NewTicker(checkEvery)
	defer t.Stop()
	for {
		if len(config.Get().Deck.Computers) > 0 || len(f.Computers()) > 0 {
			f.checkComputers()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-f.check:
		}
	}
}

// recheck asks the paired computers again soon.
func (f *Feature) recheck() {
	select {
	case f.check <- struct{}{}:
	default:
	}
}

// Reload picks up changed settings now: the setup page calls it after saving.
func (f *Feature) Reload() {
	select {
	case f.reload <- struct{}{}:
	default:
	}
}

// follow compares the settings with what is in use: a new OBS restarts the connection, and a new set
// of shown/hidden buttons is tracked.
func (f *Feature) follow() {
	d := config.Get().Deck
	conf := obsws.Config{Addr: d.OBS.Addr, Password: d.OBS.Password}
	items := trackedItems(d)

	f.mu.Lock()
	restart := conf != f.conf
	f.conf = conf
	retrack := !slices.Equal(items, f.tracked)
	f.tracked = items
	f.mu.Unlock()

	if retrack {
		f.obs.Track(items)
	}
	if restart {
		f.obs.Restart()
		f.Changed.Emit(struct{}{})
	}
}

func (f *Feature) obsConfig() obsws.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conf
}

// trackedItems are the sources the deck's show/hide buttons act on, each once.
func trackedItems(d config.Deck) []obsws.Item {
	var items []obsws.Item
	for _, p := range d.Pages {
		for _, b := range p.Buttons {
			if b.Action != config.DeckOBSSource || b.Scene == "" || b.Source == "" {
				continue
			}
			it := obsws.Item{Scene: b.Scene, Source: b.Source}
			if !slices.Contains(items, it) {
				items = append(items, it)
			}
		}
	}
	return items
}

// OBS is what is known about OBS now.
func (f *Feature) OBS() obsws.State { return f.obs.State() }

// Set is whether there is a deck to open: until there is, the bottom edge is the volume as always.
func (f *Feature) Set() bool { return config.Get().Deck.Set() }

// ErrEmpty is a press on a square with nothing on it.
var ErrEmpty = errors.New("deck: nothing on that button")

// Press does what page p's button i does.
func (f *Feature) Press(p, i int) error {
	b := config.Get().Deck.Button(p, i)
	ctx, cancel := context.WithTimeout(context.Background(), pressTimeout)
	defer cancel()
	err := f.do(ctx, b)
	if err != nil && !errors.Is(err, ErrEmpty) {
		slog.Info("deck: press failed", "page", p+1, "button", i+1, "action", b.Action, "err", err)
	}
	return err
}

func (f *Feature) do(ctx context.Context, b config.DeckButton) error {
	switch b.Action {
	case config.DeckOBSScene:
		return f.obs.SetScene(ctx, b.Scene)
	case config.DeckOBSStream:
		return f.obs.ToggleStream(ctx)
	case config.DeckOBSRecord:
		return f.obs.ToggleRecord(ctx)
	case config.DeckOBSMute:
		return f.obs.ToggleMute(ctx, b.Input)
	case config.DeckOBSSource:
		return f.obs.ToggleShown(ctx, obsws.Item{Scene: b.Scene, Source: b.Source})
	}
	if b.Action.OnComputer() {
		return f.pcDo(b)
	}
	return ErrEmpty
}

// Lit is whether a button shows as on: its scene is live, OBS is streaming or recording, its input
// is muted, its source is shown. Known is false while its state isn't known (OBS away, an input or
// source OBS doesn't have, a computer that isn't answering), and the button is then drawn dimmed.
// A computer button is never lit: pressing keys has no state to show.
func Lit(b config.DeckButton, st obsws.State, pcs map[string]ComputerState) (lit, known bool) {
	if b.Action == config.DeckNone {
		return false, false
	}
	if b.Action.OnComputer() {
		s, ok := pcs[b.Computer]
		return false, ok && s.Hello != nil
	}
	if !st.Connected {
		return false, false
	}
	switch b.Action {
	case config.DeckOBSScene:
		return st.Scene == b.Scene, slices.Contains(st.Scenes, b.Scene)
	case config.DeckOBSStream:
		return st.Streaming, true
	case config.DeckOBSRecord:
		return st.Recording, true
	case config.DeckOBSMute:
		m, ok := st.Muted[b.Input]
		return m, ok
	case config.DeckOBSSource:
		v, ok := st.Shown[obsws.Item{Scene: b.Scene, Source: b.Source}]
		return v, ok
	}
	return false, false
}

// Icon is a button's icon: its own, or its action's, changing with its state where that says
// something (a muted microphone, a stream that's live).
func Icon(b config.DeckButton, lit bool) string {
	if b.Icon != "" {
		return b.Icon
	}
	switch b.Action {
	case config.DeckOBSScene:
		return "monitor"
	case config.DeckOBSStream:
		if lit {
			return "access-point"
		}
		return "access-point-off"
	case config.DeckOBSRecord:
		if lit {
			return "record-rec"
		}
		return "record"
	case config.DeckOBSMute:
		if lit {
			return "microphone-off"
		}
		return "microphone"
	case config.DeckOBSSource:
		if lit {
			return "eye"
		}
		return "eye-off"
	case config.DeckPCKeys:
		return "keyboard"
	case config.DeckPCType:
		return "form-textbox"
	case config.DeckPCOpen:
		return "open-in-app"
	case config.DeckPCRun:
		return "script-text-play"
	}
	return ""
}

// Label is a button's words: its own, or a name from what it acts on.
func Label(b config.DeckButton) string {
	if b.Label != "" {
		return b.Label
	}
	switch b.Action {
	case config.DeckOBSScene:
		return b.Scene
	case config.DeckOBSStream:
		return "Stream"
	case config.DeckOBSRecord:
		return "Record"
	case config.DeckOBSMute:
		return b.Input
	case config.DeckOBSSource:
		return b.Source
	case config.DeckPCKeys:
		return strings.ToUpper(b.Value)
	case config.DeckPCType:
		// Never the text itself: it can be a password, and the screen can be seen from across a room.
		return "Type text"
	case config.DeckPCOpen, config.DeckPCRun:
		return b.Value
	}
	return ""
}

func (f *Feature) Actions() []*esphome.Action {
	return []*esphome.Action{{
		// Presses a deck button, counting pages and buttons from 1 as the screen shows them (buttons
		// row by row), so an automation can do what a finger does.
		Name: "deck_press",
		Args: []esphome.Arg{{Name: "page", Type: esphome.ArgInt}, {Name: "button", Type: esphome.ArgInt}},
		Run: func(c esphome.Call) (any, error) {
			p, i := c.Int("page"), c.Int("button")
			if p < 1 || i < 1 {
				return nil, fmt.Errorf("deck: pages and buttons count from 1")
			}
			return nil, f.Press(p-1, i-1)
		},
	}}
}
