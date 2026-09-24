//go:build !dot

package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A drawn dashboard is sections laid out in columns, as Home Assistant lays out its own, each a stack
// of blocks: headings, grids of tiles, cards of rows, text, graphs, gauges, pictures. It is built from
// what Home Assistant says and rebuilt whenever any of it changes. Where it comes from is a source: the
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
	// Switch draws a row's tap as a switch at its end, for something that is on or off.
	Switch bool
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

// Block is one piece of a section: one of a heading, tiles, a card of rows, text, a graph, a gauge
// or a picture. Title is a card's own title, drawn inside it.
type Block struct {
	Heading string // a heading, with Right at its other end
	Right   string
	Tiles   []Tile
	Rows    []Tile // an entities card: one thing a line
	Title   string
	Text    []string // paragraphs
	Graph   *Graph
	Gauge   *Gauge
	Picture *Picture
}

// Section is blocks that stay together, one column wide: a room, a section of a sections view, a card
// of a masonry view.
type Section struct {
	Blocks []Block
}

// Graph is a sensor's recent history as a line.
type Graph struct {
	Name, Icon, Value string
	Points            []float64 // oldest first; NaN where there is nothing yet
}

// Gauge is a reading on an arc between its ends.
type Gauge struct {
	Name, Value string
	Frac        float64 // 0 to 1 along the arc
	Severity    string  // green, yellow, red, or empty for the accent
}

// Picture is an image: a camera's latest snapshot, or a picture card's.
type Picture struct {
	Name  string
	Image image.Image // nil until it has arrived
}

// Drawn is the page as it stands.
type Drawn struct {
	Sections []Section
	Theme    Theme
	Problem  string
	Version  uint64
}

// needs is what a source asks for once connected: entities to follow, templates to render, histories
// for graphs (entity to hours), pictures to fetch.
type needs struct {
	entities  []string
	templates []template
	graphs    map[string]int
	pictures  []wanted
}

// look is everything a source draws from.
type look struct {
	states   map[string]hass.LiveEntity
	rendered map[int]string
	history  map[string][]point
	pictures map[string]image.Image
	width    int // the screen's, for a card shown only on a wide or a narrow one
}

// source is where a drawn dashboard comes from.
type source interface {
	load(ctx context.Context, live *hass.Live) (needs, error)
	sections(l look) []Section
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
	history  map[string][]point
	graphs   map[string]int // entity to hours shown, for trimming its history
	pictures map[string]image.Image
	width    int
	reload   bool // the connection was closed to read the dashboard again, not because it failed
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

// Drawn is the drawn dashboard for a screen width wide, connecting to Home Assistant if it is not
// already: the Rooms dashboard, or the one chosen. For the page that is up; CloseDrawn ends it.
func (f *Feature) Drawn(width int) Drawn {
	d := config.Get().Dashboard
	path := d.Path
	for _, k := range d.Known {
		if k.Path == path && k.Streamed {
			f.CloseDrawn()
			return Drawn{Problem: "This page can only be streamed: set Dashboard to Streamed, or pick another."}
		}
	}
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
		s = &session{f: f, src: src, width: width, states: map[string]hass.LiveEntity{}, rendered: map[int]string{}, pending: map[string]pending{},
			history: map[string][]point{}, graphs: map[string]int{}, pictures: map[string]image.Image{}}
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
			s.mu.Lock()
			quiet := s.stopped || s.reload
			s.mu.Unlock()
			if !quiet { // closing it for another dashboard, or to read this one again, is not worth a line
				slog.Info("drawn dashboard", "err", err)
			}
		}
		s.mu.Lock()
		stopped, reload := s.stopped, s.reload
		s.reload = false
		s.mu.Unlock()
		if stopped {
			return
		}
		if reload {
			wait = time.Second
			continue
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
	need, err := s.src.load(lctx, live)
	theme := loadTheme(lctx, live)
	longest := 0
	var graphed []string
	for id, h := range need.graphs {
		graphed = append(graphed, id)
		longest = max(longest, h)
	}
	history := loadHistory(lctx, live, graphed, longest)
	lcancel()
	if err != nil {
		s.problem(err.Error())
		return err
	}
	s.mu.Lock()
	s.view.Problem, s.view.Theme = "", theme
	s.history, s.graphs = history, need.graphs
	s.mu.Unlock()
	s.publish()

	go keepPictures(ctx, need.pictures, func(key string, img image.Image) {
		s.mu.Lock()
		s.pictures[key] = img
		s.mu.Unlock()
		s.publish()
	})

	fctx, fcancel := context.WithTimeout(ctx, 30*time.Second)
	defer fcancel()
	ids := need.entities
	if len(ids) > 0 {
		err = live.FollowEntities(fctx, ids, func(e hass.LiveEntity) {
			s.mu.Lock()
			s.states[e.ID] = e
			delete(s.pending, e.ID)
			if hours, graphed := s.graphs[e.ID]; graphed {
				if v, err := strconv.ParseFloat(e.State, 64); err == nil {
					h := append(s.history[e.ID], point{at: time.Now(), v: v})
					cut := time.Now().Add(-time.Duration(hours) * time.Hour)
					for len(h) > 2 && h[1].at.Before(cut) {
						h = h[1:]
					}
					s.history[e.ID] = h
				}
			}
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
	for _, t := range need.templates {
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
	// A dashboard saved in Home Assistant's editor is read again: the connection is closed, and run
	// opens a new one, which loads it as it now is.
	if l, ok := s.src.(*lovelaceSource); ok {
		mine := l.dashboard
		if mine == "" || mine == "lovelace" {
			mine = ""
		}
		if err := live.SubscribeEvents(fctx, "lovelace_updated", func(data map[string]any) {
			changed, _ := data["url_path"].(string)
			if changed == mine {
				slog.Info("drawn dashboard: changed in Home Assistant, reading it again", "dashboard", l.dashboard)
				s.mu.Lock()
				s.reload = true
				s.mu.Unlock()
				live.Close()
			}
		}); err != nil {
			slog.Info("drawn dashboard: can't hear dashboard edits", "err", err)
		}
	}
	slog.Info("drawn dashboard following", "entities", len(ids), "templates", len(need.templates),
		"graphs", len(need.graphs), "pictures", len(need.pictures), "theme", theme.Set)
	<-live.Done()
	return live.Err()
}

// publish rebuilds the page from what has arrived, and says there is something new to draw.
func (s *session) publish() {
	s.mu.Lock()
	sections := s.src.sections(look{states: s.states, rendered: s.rendered, history: s.history, pictures: s.pictures, width: s.width})
	for i := range sections {
		for j := range sections[i].Blocks {
			b := &sections[i].Blocks[j]
			for k := range b.Tiles {
				s.pendingOn(&b.Tiles[k])
			}
			for k := range b.Rows {
				s.pendingOn(&b.Rows[k])
			}
		}
	}
	s.view.Sections = sections
	s.view.Version++
	s.mu.Unlock()
	s.f.Changed.Emit(struct{}{})
}

// pendingOn shows on a tile what was asked of its entity and not yet confirmed. With s.mu held.
func (s *session) pendingOn(t *Tile) {
	entity := ""
	switch {
	case t.Tap != nil:
		entity = t.Tap.Entity
	case t.Adjust != nil:
		entity = t.Adjust.Entity
	}
	p, ok := s.pending[entity]
	if entity == "" || !ok || time.Since(p.at) > 10*time.Second {
		return
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
