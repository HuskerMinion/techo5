//go:build !dot && !spot

package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A drawn dashboard is blocks in a column - headings, grids of tiles, text - built from what Home
// Assistant says and rebuilt whenever any of it changes. Where the blocks come from is a source: the
// Rooms dashboard, or one of Home Assistant's own dashboards read card by card.

// Tile is one thing on the page: an icon, a name, and what it is doing.
type Tile struct {
	Name  string
	Icon  string // an mdi name
	Value string // what it is doing, in words: "On · 60%", "Closed", "72°"
	On    bool   // lit, open, playing: drawn in the accent color
	Gone  bool   // unavailable
	Tap   *Action
	// Adjust is a level a finger sliding along the tile sets: a light's brightness, a cover's
	// position, a thermostat's temperature.
	Adjust *Adjust
}

// Adjust is a tile's level: what it is now and how far it goes.
type Adjust struct {
	Entity   string
	Kind     string // brightness, position, temperature
	Value    float64
	Min, Max float64
	Step     float64
}

// Label is a level in words, as the tile shows it while it moves.
func (a Adjust) Label(v float64) string {
	if a.Kind == "temperature" {
		return trimNumber(v) + "°"
	}
	return fmt.Sprintf("%d%%", int(v+0.5))
}

// Snap is v on the level's steps, within its range.
func (a Adjust) Snap(v float64) float64 {
	v = min(max(v, a.Min), a.Max)
	if a.Step > 0 {
		v = a.Min + float64(int((v-a.Min)/a.Step+0.5))*a.Step
	}
	return v
}

// Action is what a tap on a tile does.
type Action struct {
	Entity  string         // the entity a toggle or service is about
	Service string         // "light.toggle"; empty for none
	Data    map[string]any // the service's data, with entity_id added when it has none
	View    string         // a dashboard view to go to instead, as a path: "home-refresh/climate"
}

// Block is one piece of the page.
type Block struct {
	Heading string // a heading, with Right at its other end
	Right   string
	Tiles   []Tile
	Text    []string // paragraphs
}

// Drawn is the page as it stands.
type Drawn struct {
	Blocks  []Block
	Problem string
	Version uint64
}

// source is where a drawn dashboard's blocks come from.
type source interface {
	// load reads what the dashboard needs once connected: which entities to follow and which
	// templates to have Home Assistant render.
	load(ctx context.Context, live *hass.Live) (entities []string, templates []template, err error)
	// blocks is the page, from the entities' states and the templates as last rendered.
	blocks(states map[string]hass.LiveEntity, rendered map[int]string) []Block
}

// template is text Home Assistant renders and renders again whenever what it reads changes: a
// markdown card's content, a Mushroom card's words.
type template struct {
	id   int
	text string
	vars map[string]any
}

// session is the live connection behind a drawn page.
type session struct {
	f   *Feature
	src source

	mu       sync.Mutex
	live     *hass.Live
	stopped  bool
	states   map[string]hass.LiveEntity
	rendered map[int]string
	pending  map[string]pending
	view     Drawn
}

// pending is what a tap or a slide asked of an entity, shown at once rather than when Home
// Assistant confirms it: a light that fades takes a second or two to say it is on, and a tile that
// waits for that feels broken. What Home Assistant says next replaces it.
type pending struct {
	at    time.Time
	flip  bool   // toggled: shown the other way
	value string // set to a level: shown as this
	on    bool
}

// Drawn is the drawn dashboard, connecting to Home Assistant if it is not already: the Rooms
// dashboard, or the one chosen. For the page that is up; CloseDrawn ends it.
func (f *Feature) Drawn() Drawn {
	path := config.Get().Dashboard.Path
	f.mu.Lock()
	s := f.drawn
	if s == nil || f.drawnPath != path {
		if s != nil {
			go s.close()
		}
		var src source = &roomsSource{}
		if path != "" {
			src = newLovelace(path)
		}
		s = &session{f: f, src: src, states: map[string]hass.LiveEntity{}, rendered: map[int]string{}, pending: map[string]pending{}}
		f.drawn, f.drawnPath = s, path
		go s.run()
	}
	f.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view
}

// CloseDrawn ends the drawn session, if there is one.
func (f *Feature) CloseDrawn() {
	f.mu.Lock()
	s := f.drawn
	f.drawn = nil
	f.mu.Unlock()
	if s != nil {
		s.close()
	}
}

// Tap does what a tile's action says.
func (f *Feature) Tap(a Action) {
	if a.View != "" {
		// A navigation path names the dashboard as well as the view: "home-refresh/climate".
		if err := config.Set().Dashboard().Path(strings.Trim(a.View, "/")); err != nil {
			slog.Warn("dashboard: going to a view failed", "err", err)
		}
		f.listBoards(config.Get().Dashboard)
		f.Changed.Emit(struct{}{})
		return
	}
	f.mu.Lock()
	s := f.drawn
	f.mu.Unlock()
	if s == nil || a.Service == "" {
		return
	}
	domain, service, ok := strings.Cut(a.Service, ".")
	if !ok {
		return
	}
	data := map[string]any{}
	for k, v := range a.Data {
		data[k] = v
	}
	if _, has := data["entity_id"]; !has && a.Entity != "" {
		data["entity_id"] = a.Entity
	}
	var p *pending
	if strings.HasSuffix(service, "toggle") || service == "media_play_pause" {
		p = &pending{flip: true}
	}
	s.call(a.Entity, domain, service, data, p)
}

// SetLevel sets a tile's level to v: a light's brightness, a cover's position, a thermostat's
// temperature.
func (f *Feature) SetLevel(a Adjust, v float64) {
	f.mu.Lock()
	s := f.drawn
	f.mu.Unlock()
	if s == nil {
		return
	}
	v = a.Snap(v)
	data := map[string]any{"entity_id": a.Entity}
	var domain, service string
	switch a.Kind {
	case "brightness":
		domain, service = "light", "turn_on"
		data["brightness_pct"] = int(v + 0.5)
		if v <= 0 {
			service = "turn_off"
			delete(data, "brightness_pct")
		}
	case "position":
		domain, service = "cover", "set_cover_position"
		data["position"] = int(v + 0.5)
	case "temperature":
		domain, service = "climate", "set_temperature"
		data["temperature"] = v
	default:
		return
	}
	s.call(a.Entity, domain, service, data, &pending{value: a.Label(v), on: v > a.Min})
}

// call runs a service for a tile, showing what it asked for until Home Assistant says otherwise.
func (s *session) call(entity, domain, service string, data map[string]any, p *pending) {
	s.mu.Lock()
	live := s.live
	if p != nil && entity != "" {
		p.at = time.Now()
		s.pending[entity] = *p
	}
	s.mu.Unlock()
	s.publish()
	if live == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := live.CallService(ctx, domain, service, data); err != nil {
			slog.Warn("dashboard: a tap failed", "service", domain+"."+service, "err", err)
			s.mu.Lock()
			delete(s.pending, entity)
			s.mu.Unlock()
			s.publish()
		}
	}()
}

func (s *session) close() {
	s.mu.Lock()
	s.stopped = true
	live := s.live
	s.mu.Unlock()
	if live != nil {
		live.Close()
	}
}

func (s *session) problem(text string) {
	s.mu.Lock()
	s.view.Problem = text
	s.view.Version++
	s.mu.Unlock()
	s.f.Changed.Emit(struct{}{})
}

// run keeps a session going until closed.
func (s *session) run() {
	wait := time.Second
	for {
		s.mu.Lock()
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			return
		}
		if err := s.once(); err != nil {
			slog.Info("drawn dashboard", "err", err)
		}
		s.mu.Lock()
		stopped = s.stopped
		s.mu.Unlock()
		if stopped {
			return
		}
		time.Sleep(wait)
		wait = min(wait*2, 30*time.Second)
	}
}

func (s *session) once() error {
	if !hass.Get().Ready() {
		s.problem("A drawn dashboard needs Home Assistant's address and a token (the hass action).")
		return fmt.Errorf("no home assistant access")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live, err := hass.Get().OpenLive(ctx)
	if err != nil {
		s.problem("Can't reach Home Assistant.")
		return err
	}
	defer live.Close()
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.live = live
	s.mu.Unlock()

	lctx, lcancel := context.WithTimeout(ctx, 30*time.Second)
	ids, templates, err := s.src.load(lctx, live)
	lcancel()
	if err != nil {
		s.problem(err.Error())
		return err
	}
	s.mu.Lock()
	s.view.Problem = ""
	s.mu.Unlock()
	s.publish()

	fctx, fcancel := context.WithTimeout(ctx, 30*time.Second)
	defer fcancel()
	if len(ids) > 0 {
		err = live.FollowEntities(fctx, ids, func(e hass.LiveEntity) {
			s.mu.Lock()
			s.states[e.ID] = e
			delete(s.pending, e.ID)
			s.mu.Unlock()
			s.publish()
		}, func(id string) {
			s.mu.Lock()
			delete(s.states, id)
			s.mu.Unlock()
			s.publish()
		})
		if err != nil {
			return err
		}
	}
	for _, t := range templates {
		id := t.id
		if err := live.RenderTemplate(fctx, t.text, t.vars, func(result string) {
			s.mu.Lock()
			s.rendered[id] = result
			s.mu.Unlock()
			s.publish()
		}); err != nil {
			slog.Info("drawn dashboard: a template would not render", "err", err)
		}
	}
	slog.Info("drawn dashboard following", "entities", len(ids), "templates", len(templates))
	<-live.Done()
	return live.Err()
}

// publish rebuilds the page from what has arrived, and says there is something new to draw.
func (s *session) publish() {
	s.mu.Lock()
	blocks := s.src.blocks(s.states, s.rendered)
	for i := range blocks {
		for j := range blocks[i].Tiles {
			t := &blocks[i].Tiles[j]
			entity := ""
			switch {
			case t.Tap != nil:
				entity = t.Tap.Entity
			case t.Adjust != nil:
				entity = t.Adjust.Entity
			}
			p, ok := s.pending[entity]
			if entity == "" || !ok || time.Since(p.at) > 10*time.Second {
				continue
			}
			switch {
			case p.flip:
				t.On = !t.On
				switch t.Value {
				case "On":
					t.Value = "Off"
				case "Off":
					t.Value = "On"
				default:
					if t.On {
						t.Value = "On"
					} else {
						t.Value = "Off"
					}
				}
			case p.value != "":
				t.On = p.on
				t.Value = p.value
				if t.Adjust != nil && t.Adjust.Kind == "brightness" {
					t.Value = "On · " + p.value
					if !p.on {
						t.Value = "Off"
					}
				}
			}
		}
	}
	s.view.Blocks = blocks
	s.view.Version++
	s.mu.Unlock()
	s.f.Changed.Emit(struct{}{})
}

// raw is a card's JSON as it came, read field by field.
type raw = map[string]any

func str(m raw, key string) string {
	v, _ := m[key].(string)
	return v
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
