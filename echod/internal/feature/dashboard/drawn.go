//go:build !dot

package dashboard

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// A drawn dashboard is sections laid out in columns, as Home Assistant lays out its own, each a stack
// of blocks: headings, grids of tiles, cards of rows, text, graphs, gauges, pictures. It is built from
// what Home Assistant says and rebuilt whenever any of it changes. Where it comes from is a source: the
// Rooms dashboard, or one of Home Assistant's own dashboards read card by card.

// Tile is one thing on the page: an icon, a name, and what it is doing.
type Tile struct {
	// Entity is the entity the tile shows, when it shows one: what a long press on it opens a sheet
	// for (a light's colors, a media player's music).
	Entity string
	Name   string
	Icon   string // an mdi name
	Value  string // what it is doing, in words: "On · 60%", "Closed", "72°"
	On     bool   // lit, open, playing: drawn in the accent color
	Gone   bool   // unavailable
	Tap    *Action
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
	Data    map[string]any // the service's data, with entity_id added when it has none (serviceData)
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
	// Pictures is a gallery: a grid of nothing but pictures, laid out abreast as Columns asks, each
	// picture a tap of its own.
	Pictures []Picture
	Columns  int
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
	Name     string
	Image    image.Image // nil until it has arrived
	TooLarge bool        // it arrived, larger than the device will decode
	Failed   bool        // it could not be fetched or read, and will be asked for again
	// Shape is its own size when it is not drawn but its size is known: a picture too large to decode.
	// A picture with elements still has them placed on a frame of that shape.
	Shape image.Point
	Tap   *Action // the card's own tap_action; nil when a tap does nothing
	// Elements are a picture-elements card's: places on the picture, each a tap of its own, some
	// showing an entity. A picture with them is drawn whole, so they stay where they were put.
	Elements []Element
}

// Element is one of a picture-elements card's elements, placed as its style places it in Home
// Assistant: left and top name a point on the picture, and the element is moved by its own size from
// there, by half of it unless the style's transform says otherwise, so that it is centered on it.
type Element struct {
	X, Y           float64 // the point, as fractions of the picture's width and height
	W, H           float64 // its size, as fractions of the picture; 0 when the style gives none
	ShiftX, ShiftY float64 // how far it moves by its own size: -0.5 each, as Home Assistant has it
	Icon           string  // an icon's or a state icon's; empty for a place that is only tapped
	Label          string  // a state label's words, or a service button's
	On, Gone       bool
	Entity         string
	Tap            *Action // nil when a tap does nothing
	// Color is the style's color for its icon or words; zero for the theme's.
	Color color.RGBA
	// Hidden is an element its style makes invisible (opacity 0, a transparent color): only a tap.
	Hidden bool
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
	pictures map[string]shot
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
	pictures map[string]shot
	width    int
	reload   bool // the connection was closed to read the dashboard again, not because it failed
	view     Drawn
	gen      int // f.recent's when the session began (recent.put)

	// hold is the page that was up when a tap went to this view, shown instead of this one until it
	// is ready (ready) or until holdUntil, whichever is first: a view and its picture take a fraction
	// of a second, and the page between, then "Loading the picture…", only flicker.
	hold      *Drawn
	holdUntil time.Time
	loaded    bool     // the dashboard has been read, and the pictures it shows are known
	wide      []string // its pictures kept as wide as the screen, by key: what a held page waits for

	// soon is a rebuild waiting to happen: a house changes a lot of entities at once, and the page
	// is rebuilt a few times a second at most, not once for each of them.
	soon *time.Timer
}

// publishEvery is how often the page is rebuilt at most while entities change.
const publishEvery = 250 * time.Millisecond

// publishSoon rebuilds the page within publishEvery, once however many changes arrive meanwhile.
func (s *session) publishSoon() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.soon == nil {
		s.soon = time.AfterFunc(publishEvery, func() {
			s.mu.Lock()
			s.soon = nil
			s.mu.Unlock()
			s.publish()
		})
	}
}

// pending is what a tap or a slide asked of an entity, shown at once rather than when Home
// Assistant confirms it: a light that fades takes a second or two to say it is on, and a tile that
// waits for that feels broken. What Home Assistant says next replaces it.
type pending struct {
	at    time.Time
	flip  bool   // toggled: shown the other way
	value string // set to a level: shown as this
	on    bool
	// keepOn leaves whether the tile is lit alone: a level that is not the thing being on.
	keepOn bool
}

// Drawn is the drawn dashboard for a screen width wide, connecting to Home Assistant if it is not
// already: the Rooms dashboard, or the one chosen. For the page that is up; CloseDrawn ends it.
func (f *Feature) Drawn(width int) Drawn {
	d := config.Get().Dashboard
	path := d.Path
	_, web := WebPage(path) // a dashcast web page, listed or no longer
	for _, k := range d.Known {
		web = web || k.Path == path && k.Streamed
	}
	if web {
		f.CloseDrawn()
		return Drawn{Problem: "This page can only be streamed: set Dashboard to Streamed, or pick another."}
	}
	f.mu.Lock()
	s := f.drawn
	if s == nil || f.drawnPath != path {
		var held *Drawn
		if s != nil {
			// A tap that went to this view keeps the page it was tapped on up until this one is ready.
			// Any other change, a dashboard chosen in setup or from Home Assistant, starts afresh and lets
			// the last one's pictures go.
			if f.navTo == path {
				s.mu.Lock()
				old := s.shown()
				s.mu.Unlock()
				held = &old
			} else {
				f.recent.empty()
			}
			safe.Go("drawn dashboard close", s.close)
		}
		var src source = &roomsSource{}
		if path != "" {
			src = newLovelace(path)
		}
		s = &session{f: f, src: src, width: width, states: map[string]hass.LiveEntity{}, rendered: map[int]string{}, pending: map[string]pending{},
			history: map[string][]point{}, graphs: map[string]int{}, pictures: map[string]shot{}, gen: f.recent.generation()}
		if held != nil {
			s.hold, s.holdUntil = held, time.Now().Add(holdFor)
			// Nothing else may say there is something new by then: a dashboard that never loads.
			time.AfterFunc(holdFor, func() { f.Changed.Emit(struct{}{}) })
		}
		f.drawn, f.drawnPath = s, path
		run := runDrawn
		safe.Go("drawn dashboard", func() { run(s) })
	}
	if f.navTo == path {
		f.navTo = ""
	}
	f.drawnUsed = time.Now()
	f.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shown()
}

// holdFor is the longest a page is held after a tap went to another view: long enough for a view
// and its pictures on a slow link, short enough that a picture that never comes doesn't leave the
// tap looking undone.
const holdFor = 1500 * time.Millisecond

// runDrawn keeps a session going; a variable so that a test can drive one by hand, without a Home
// Assistant.
var runDrawn = (*session).run

// shown is the page to draw: the one held from before a tap, while it is held, or this one. With
// s.mu held.
func (s *session) shown() Drawn {
	if s.hold != nil {
		if time.Now().Before(s.holdUntil) {
			return *s.hold
		}
		s.hold = nil
	}
	return s.view
}

// ready is whether the page can be shown in place of a held one: read, and with every picture kept
// as wide as the screen there, or said to be too large or not loading. A picture on a card shows
// "Loading" on its card alone, so it is not waited for. With s.mu held.
func (s *session) ready() bool {
	if !s.loaded {
		return false
	}
	for _, k := range s.wide {
		if _, has := s.pictures[k]; !has {
			return false
		}
	}
	return true
}

// seed starts the pictures from the ones kept between pages (recent), so a view opened again draws
// at once while its pictures are fetched again behind it, and notes what a held page waits for.
// With s.mu held.
func (s *session) seed(list []wanted) {
	s.wide = nil
	for _, w := range list {
		k := w.key()
		if _, has := s.pictures[k]; !has {
			if sh, ok := s.f.recent.get(k); ok {
				s.pictures[k] = sh
			}
		}
		if w.wide {
			s.wide = append(s.wide, k)
		}
	}
	s.loaded = true
}

// got is a picture as it arrives. While a page is held for this one, it is rebuilt at once rather
// than within publishEvery: the picture may be all it was waiting for.
func (s *session) got(key string, sh shot) {
	s.mu.Lock()
	// A camera's snapshot missed once leaves the last one up rather than saying it failed.
	if !sh.failed || s.pictures[key].img == nil {
		s.pictures[key] = sh
	}
	kept, holding := s.pictures[key], s.hold != nil
	s.mu.Unlock()
	s.f.recent.put(s.gen, key, kept)
	if holding {
		s.publish()
	} else {
		s.publishSoon()
	}
}

// CloseDrawn ends the drawn session, if there is one.
func (f *Feature) CloseDrawn() {
	f.mu.Lock()
	s := f.drawn
	f.drawn, f.navTo = nil, ""
	// Under f.mu, so a session asked for right after starts with the new generation.
	f.recent.empty()
	f.mu.Unlock()
	if s != nil {
		s.close()
	}
}

// Tap does what a tile's action says.
func (f *Feature) Tap(a Action) {
	if a.View != "" {
		// A navigation path names the dashboard as well as the view: "home-refresh/climate".
		path := navPath(a.View)
		f.mu.Lock()
		f.navTo = path
		f.mu.Unlock()
		if err := config.Set().Dashboard().Path(path); err != nil {
			slog.Warn("dashboard: going to a view failed", "err", err)
			// A save that failed on the flash still changed the path in memory, and the view
			// changes all the same: it is still a navigate unless the path did not move.
			if config.Get().Dashboard.Path != path {
				f.mu.Lock()
				f.navTo = ""
				f.mu.Unlock()
			}
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
	data := serviceData(a)
	// A media player's play or pause, next and back reach the whole group when Music Assistant plays
	// there (transportTo). Which player that is takes a look at Home Assistant, so it is worked out with
	// the call, off the goroutine the finger is on. Only the tile's own player is routed: a card that
	// names a target of its own ("pause everywhere") keeps it.
	var route func() string
	own, _ := data["entity_id"].(string)
	other := false // a target of the card's own besides entities
	for _, k := range []string{"area_id", "device_id", "floor_id", "label_id"} {
		if _, has := data[k]; has {
			other = true
		}
	}
	if domain == "media_player" && a.Entity != "" && own == a.Entity && !other &&
		(service == "media_play_pause" || service == "media_next_track" || service == "media_previous_track") {
		route = func() string { return routeTransport(a.Entity) }
	}
	var p *pending
	if strings.HasSuffix(service, "toggle") || service == "media_play_pause" {
		p = &pending{flip: true}
	}
	s.callVia(a.Entity, domain, service, data, p, route)
}

// serviceData is what a tap's service is called with: the card's data and target, and the card's
// entity as the target when it names none. Not for an ESPHome device's own action
// (esphome.<node>_home_show_camera, say): it takes no entity, refuses any argument it does not have,
// and a picture of a camera whose tap showed that camera on a device failed with entity_id added.
func serviceData(a Action) map[string]any {
	data := map[string]any{}
	for k, v := range a.Data {
		data[k] = v
	}
	if _, has := data["entity_id"]; !has && a.Entity != "" && !strings.HasPrefix(a.Service, "esphome.") {
		data["entity_id"] = a.Entity
	}
	return data
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
	case "volume":
		domain, service = "media_player", "volume_set"
		data["volume_level"] = float64(int(v+0.5)) / 100
	default:
		return
	}
	// A player's volume says nothing about whether it is playing: the tile keeps that.
	s.call(a.Entity, domain, service, data, &pending{value: a.Label(v), on: v > a.Min, keepOn: a.Kind == "volume"})
}

// call runs a service for a tile, showing what it asked for until Home Assistant says otherwise.
func (s *session) call(entity, domain, service string, data map[string]any, p *pending) {
	s.callVia(entity, domain, service, data, p, nil)
}

// callService runs a service on Home Assistant; a variable so that a test can see what a tap asks for,
// and when, without a Home Assistant.
var callService = func(live *hass.Live, ctx context.Context, domain, service string, data map[string]any) error {
	return live.CallService(ctx, domain, service, data)
}

// callVia is call with the entity the service goes to worked out by route, when there is one, on the
// way: with the service, never on the caller's goroutine, which is the finger's.
func (s *session) callVia(entity, domain, service string, data map[string]any, p *pending, route func() string) {
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
		// The route first: it may wait on Home Assistant's REST side, and the call's own time is
		// for the call.
		if route != nil {
			data["entity_id"] = route()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := callService(live, ctx, domain, service, data); err != nil {
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
	if s.soon != nil {
		s.soon.Stop()
		s.soon = nil
	}
	s.mu.Unlock()
	if live != nil {
		live.Close()
	}
}

func (s *session) problem(text string) {
	s.mu.Lock()
	s.view.Problem = text
	s.view.Version++
	s.hold = nil // what went wrong is said now, not after the held page's time is up
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
		s.problem("A drawn dashboard needs Home Assistant's address and a token (the home_assistant action).")
		return fmt.Errorf("no home assistant access")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live, err := hass.Get().OpenLive(ctx)
	if err != nil {
		// The address the home_assistant action gave is kept until it is given again: a Home
		// Assistant that moved is the commonest reason for this, so say where it was looked for.
		s.problem(unreachable(hass.Get().Address()))
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
	s.seed(need.pictures)
	s.mu.Unlock()
	s.publish()

	safe.Go("dashboard pictures", func() {
		keepPictures(ctx, need.pictures, s.got)
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
					s.history[e.ID] = keep(append(s.history[e.ID], point{at: time.Now(), v: v}), hours)
				}
			}
			s.mu.Unlock()
			s.publishSoon()
		}, func(id string) {
			s.mu.Lock()
			delete(s.states, id)
			s.mu.Unlock()
			s.publishSoon()
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
			s.publishSoon()
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
	if s.hold != nil && s.ready() {
		s.hold = nil
	}
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
	default:
		entity = t.Entity // a thermostat with neither: set from its sheet
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
		if !p.keepOn {
			t.On = p.on
		}
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

// onScreen is an error that is a sentence for the screen as it stands, where the page says what went
// wrong in words somebody standing at it can act on.
type onScreen string

func (s onScreen) Error() string { return string(s) }

// unreachable is what a drawn dashboard says when Home Assistant does not answer at addr.
func unreachable(addr string) string {
	if addr == "" {
		return "Can't reach Home Assistant."
	}
	return "Can't reach Home Assistant at " + addr + ". If it has moved, run the home_assistant action again with its new address."
}
