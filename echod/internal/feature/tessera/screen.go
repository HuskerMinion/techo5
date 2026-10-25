package tessera

import (
	"encoding/json"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

// The screen's side of drawing: the layout in force as a View, and what a tap or a slide on a tile asks
// of Home Assistant. A tap calls the entity's action as the device's own (component.CallService), which
// Home Assistant runs only with "Allow the device to perform Home Assistant actions" on, as the add-on's
// own screens do; the add-on sees the entity change and sends the tile's new state. Until it does, the
// tile shows what was asked, so a light that fades in does not look as if the tap missed.

// Changed fires when there is something new to draw: a layout came into force, or a tile's state, a top
// bar or a title changed. Listeners must not block.
var Changed hook.Hook[struct{}]

// pendingFor is how long a tile shows what a finger asked when Home Assistant says nothing back.
const pendingFor = 5 * time.Second

// confirmFor is how long a second tap has to confirm the first: the settings' Restart and Forget give
// as long.
const confirmFor = 4 * time.Second

// pending is what a tap or a slide asked of a tile: the state shown, or the level, until the tile's own
// state or attributes change from what they were when it was asked, or pendingFor passes. A confirm is a
// first tap waiting for the second, for confirmFor.
type pending struct {
	at        time.Time
	was, wasA string
	state     string
	level     *float64
	confirm   bool
}

// life is how long p stands.
func (p pending) life() time.Duration {
	if p.confirm {
		return confirmFor
	}
	return pendingFor
}

// holds is whether p still stands in for what the tile says.
func (p pending) holds(t Tile, now time.Time) bool {
	return now.Sub(p.at) < p.life() && t.State == p.was && string(t.A) == p.wasA
}

// over is t as the screen shows it while p stands: in the state asked for, at the level asked for. A
// confirm asks for nothing yet.
func (p pending) over(t Tile) Tile {
	if p.confirm {
		return t
	}
	t.State = p.state
	if p.level != nil {
		t.A = withLevel(t.A, t.Entity, *p.level)
	}
	return t
}

// Screen is a receiver's layout as something to draw and touch.
type Screen struct {
	recv *Receiver
	grid grid

	mu   sync.Mutex
	held map[int]pending
}

// current is the screen, while the Tessera screen switch is on.
var current atomic.Pointer[Screen]

// newScreen is the screen for recv's layouts on a grid of g.
func newScreen(recv *Receiver, g grid) *Screen {
	return &Screen{recv: recv, grid: g, held: map[int]pending{}}
}

// Current is the layout to draw, and false when the Tessera screen is off. On, with no layout yet, it
// is a View with no pages.
func Current() (View, bool) {
	s := current.Load()
	if s == nil {
		return View{}, false
	}
	return s.View(time.Now()), true
}

// Preview puts l in force on a grid of columns by rows, as the screen would hold it with the switch on:
// for a preview, and for tests of what draws it and what its sheets read. Nil puts the screen away.
func Preview(l *Layout, columns, rows int) {
	if l == nil {
		current.Store(nil)
		return
	}
	r := NewReceiver(Capabilities{MaxTiles: len(l.Tiles), MaxPages: len(l.Pages)}, nil)
	r.Restore(l)
	current.Store(newScreen(r, grid{columns: columns, rows: rows}))
}

// Tap is a tap on tile i of the layout in force, the one that showed entity where the finger was. A
// tile that shows another by now, as after a layout came between the finger landing and lifting, is
// left alone. It says true for a tile whose tap opens its card, which is the screen's to draw.
func Tap(i int, entity string) bool {
	if s := current.Load(); s != nil {
		return s.Tap(i, entity, time.Now())
	}
	return false
}

// SetLevel is a finger that slid tile i's level, of entity, to v.
func SetLevel(i int, entity string, v float64) {
	if s := current.Load(); s != nil {
		s.SetLevel(i, entity, v, time.Now())
	}
}

// Transport is the player a tap's play or pause goes to. The display makes it the drawn dashboard's
// (Music Assistant's player while it plays there), which takes a look at Home Assistant, so it is
// called off the finger's goroutine.
var Transport = func(entity string) string { return entity }

// View is the layout in force, placed on the grid, with what fingers asked shown.
func (s *Screen) View(now time.Time) View {
	s.mu.Lock()
	held := make(map[int]pending, len(s.held))
	for i, p := range s.held {
		held[i] = p
	}
	s.mu.Unlock()
	var v View
	s.recv.with(func(l *Layout) { v = view(l, s.grid, now, held) })
	return v
}

// tile is tile i of the layout in force, false unless it shows entity: as Home Assistant last said
// (raw), and as the screen shows it, with what a finger asked while that stands. What a finger does
// goes by the second, so a second quick step of a thermostat steps on from the first.
func (s *Screen) tile(i int, entity string, now time.Time) (raw, shown Tile, ok bool) {
	s.recv.with(func(l *Layout) {
		if l != nil && i >= 0 && i < len(l.Tiles) && l.Tiles[i].Entity == entity {
			raw, ok = l.Tiles[i], true
		}
	})
	if !ok {
		return raw, raw, false
	}
	s.mu.Lock()
	p, held := s.held[i]
	s.mu.Unlock()
	if held && p.holds(raw, now) {
		return raw, p.over(raw), true
	}
	return raw, raw, true
}

// Tap calls the action a tap on tile i is for, and shows its outcome until Home Assistant says. A tile
// whose tap opens its card calls nothing and says true. A locked lock is unlocked only by a second tap
// soon after the first, and not at all where the layout lets a tap only lock it; a closed garage door,
// gate or door is opened the same way. Locking and shutting take one. A player's play or pause goes
// where the drawn dashboard's does (Transport).
func (s *Screen) Tap(i int, entity string, now time.Time) bool {
	raw, t, ok := s.tile(i, entity, now)
	if !ok {
		return false
	}
	if p := describe(t, tileOptions(t)); !p.Taps {
		return false
	} else if p.Detail {
		return true
	}
	service, ok := tapService(t.Entity, t.State)
	if !ok {
		return false
	}
	if guarded(t, service) && !s.sure(i, raw, now) {
		return false
	}
	slog.Info("tessera tap", "entity", t.Entity, "service", service)
	c := component.Call{Service: service, Data: map[string]string{"entity_id": t.Entity}}
	if service == "media_player.media_play_pause" {
		route := Transport
		go func() {
			c.Data["entity_id"] = route(t.Entity)
			component.CallService.Emit(c)
		}()
	} else {
		component.CallService.Emit(c)
	}
	if next, ok := tapOutcome(t.Entity, t.State); ok {
		s.hold(i, raw, pending{state: next}, now)
	} else {
		s.drop(i) // a cover stopped: where it stopped is Home Assistant's to say
	}
	return false
}

// guarded is whether service, on t as it is shown, waits for a second tap: unlocking a lock, and
// opening a garage door, a gate or a door.
func guarded(t Tile, service string) bool {
	switch service {
	case "lock.unlock":
		return true
	case "cover.open_cover":
		switch str(attrs(t.A), "device_class") {
		case "garage", "garage_door", "gate", "door":
			return t.State != "open" && t.State != "opening"
		}
	}
	return false
}

// sure is whether a first tap on tile i, as it was at raw, asked to be sure less than confirmFor ago.
// When none did, this one is the first, and asks.
func (s *Screen) sure(i int, raw Tile, now time.Time) bool {
	s.mu.Lock()
	p, armed := s.held[i]
	s.mu.Unlock()
	if armed && p.confirm && p.holds(raw, now) {
		return true
	}
	slog.Info("tessera tap asks to be sure", "entity", raw.Entity)
	s.hold(i, raw, pending{confirm: true}, now)
	return false
}

// SetLevel calls the action that sets tile i's level to v, and shows it until Home Assistant says.
func (s *Screen) SetLevel(i int, entity string, v float64, now time.Time) {
	raw, t, ok := s.tile(i, entity, now)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	s.setLevel(i, raw, t, v, now)
}

// setLevel is SetLevel on tile i as Home Assistant last said (raw) and as it is shown (t).
func (s *Screen) setLevel(i int, raw, t Tile, v float64, now time.Time) {
	call, state, ok := levelCall(t.Entity, t.State, v)
	if !ok {
		return
	}
	slog.Info("tessera slide", "entity", t.Entity, "service", call.Service, "to", v)
	component.CallService.Emit(call)
	s.hold(i, raw, pending{state: state, level: &v}, now)
}

// drop lets go of what a finger asked of tile i.
func (s *Screen) drop(i int) {
	s.mu.Lock()
	_, had := s.held[i]
	delete(s.held, i)
	s.mu.Unlock()
	if had {
		Changed.Emit(struct{}{})
	}
}

// hold keeps p for tile i, as it was at t.
func (s *Screen) hold(i int, t Tile, p pending, now time.Time) {
	p.at, p.was, p.wasA = now, t.State, string(t.A)
	s.mu.Lock()
	s.held[i] = p
	s.mu.Unlock()
	Changed.Emit(struct{}{})
	// Drawn again once it has run out, when nothing else has said so meanwhile.
	time.AfterFunc(p.life(), func() { Changed.Emit(struct{}{}) })
}

// tapService is the action a tap on an entity in this state calls, as the add-on's own screens do: a
// toggle for what is on or off, a cover opened or closed by where it is, a scene or a script run, a
// button pressed, a lock locked or unlocked (Tap asks twice before it unlocks). A thermostat, a sensor and
// the rest have no tap here.
func tapService(entity, state string) (string, bool) {
	if state == "unavailable" {
		return "", false
	}
	d := domain(entity)
	switch d {
	case "light", "switch", "input_boolean", "fan", "automation", "humidifier", "siren":
		return d + ".toggle", true
	case "cover":
		switch state {
		case "opening", "closing":
			return "cover.stop_cover", true
		case "closed":
			return "cover.open_cover", true
		}
		return "cover.close_cover", true
	case "lock":
		switch state {
		case "unlocked", "open", "jammed":
			return "lock.lock", true
		case "locked":
			return "lock.unlock", true
		}
	case "scene", "script":
		return d + ".turn_on", true
	case "button", "input_button":
		return d + ".press", true
	case "media_player":
		if state == "off" || state == "standby" {
			return "media_player.turn_on", true
		}
		return "media_player.media_play_pause", true
	}
	return "", false
}

// tapOutcome is the state a tap leaves, shown until Home Assistant says: false where there is none to
// show, a scene or a button's.
func tapOutcome(entity, state string) (string, bool) {
	switch domain(entity) {
	case "light", "switch", "input_boolean", "fan", "automation", "humidifier", "siren":
		if state == "on" {
			return "off", true
		}
		return "on", true
	case "cover":
		switch state {
		case "closed":
			return "opening", true
		case "open":
			return "closing", true
		}
	case "lock":
		if state == "locked" {
			return "unlocking", true
		}
		return "locking", true
	case "media_player":
		switch state {
		case "playing":
			return "paused", true
		case "paused":
			return "playing", true
		}
	}
	return "", false
}

// levelCall is the action that sets an entity's level to v, and the state that leaves it in. The level
// goes as a template so that Home Assistant takes it as a number: every field of a device's call arrives
// as text.
func levelCall(entity, state string, v float64) (component.Call, string, bool) {
	n := strconv.FormatFloat(v, 'f', -1, 64)
	call := func(service, field, next string) (component.Call, string, bool) {
		return component.Call{
			Service:   service,
			Data:      map[string]string{"entity_id": entity},
			Templates: map[string]string{field: "{{ " + n + " }}"},
		}, next, true
	}
	switch domain(entity) {
	case "light":
		if v <= 0 {
			return component.Call{Service: "light.turn_off", Data: map[string]string{"entity_id": entity}}, "off", true
		}
		return call("light.turn_on", "brightness_pct", "on")
	case "cover":
		next := "open"
		if v <= 0 {
			next = "closed"
		}
		return call("cover.set_cover_position", "position", next)
	case "climate":
		return call("climate.set_temperature", "temperature", state)
	case "fan":
		if v <= 0 {
			return component.Call{Service: "fan.turn_off", Data: map[string]string{"entity_id": entity}}, "off", true
		}
		return call("fan.set_percentage", "percentage", "on")
	case "media_player":
		c, _, ok := call("media_player.volume_set", "volume_level", state)
		c.Templates["volume_level"] = "{{ " + strconv.FormatFloat(v/100, 'f', -1, 64) + " }}"
		return c, state, ok
	case "input_number", "number":
		c, _, ok := call(domain(entity)+".set_value", "value", n)
		return c, n, ok
	}
	return component.Call{}, "", false
}

// Entity is what the layout in force last heard of an entity on one of its tiles, for a sheet: its
// state, and its attributes with what a sheet reads from them put in where the add-on sends it
// elsewhere or not at all - its name, a player's artist, the color mode a light is in. False when no
// tile shows it.
func Entity(id string) (string, map[string]any, bool) {
	s := current.Load()
	if s == nil {
		return "", nil, false
	}
	var (
		t  Tile
		ok bool
	)
	s.recv.with(func(l *Layout) {
		if l == nil {
			return
		}
		for _, x := range l.Tiles {
			if x.Entity == id {
				t, ok = x, true
				return
			}
		}
	})
	if !ok {
		return "", nil, false
	}
	a := attrs(t.A)
	if _, has := a["friendly_name"]; !has && t.Name != "" {
		a["friendly_name"] = t.Name
	}
	if artist := str(attrs(t.X), "artist"); artist != "" && str(a, "media_artist") == "" {
		a["media_artist"] = artist
	}
	if domain(id) == "light" && str(a, "color_mode") == "" {
		// The add-on sends a light's white only while it is on one, and its hue whatever it is on.
		if _, white := num(a, "color_temp_kelvin"); white {
			a["color_mode"] = "color_temp"
		} else if _, hue := a["hs_color"].([]any); hue {
			a["color_mode"] = "hs"
		}
	}
	return t.State, a, true
}

// Call runs a Home Assistant action for a sheet, as a tap does: on entity, with data. Every field of a
// device's call arrives as text, so what is not text - a number, a list, a yes or no - goes as a
// template, which Home Assistant renders to the thing itself.
func Call(entity, service string, data map[string]any) {
	c := component.Call{Service: service, Data: map[string]string{}, Templates: map[string]string{}}
	for k, v := range data {
		if text, ok := v.(string); ok {
			c.Data[k] = text
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			slog.Warn("tessera: a field of a call could not be written", "service", service, "field", k, "err", err)
			continue
		}
		c.Templates[k] = "{{ " + string(b) + " }}"
	}
	_, named := c.Data["entity_id"]
	if _, listed := c.Templates["entity_id"]; !named && !listed && entity != "" {
		c.Data["entity_id"] = entity
	}
	slog.Info("tessera sheet", "entity", entity, "service", service)
	component.CallService.Emit(c)
}

// Control is a tap on one of tile i's controls, of entity: a thermostat's step down or up ("down", "up")
// or one of its modes ("mode:heat"), a cover's "open", "stop" or "close". A player's keys are the
// screen's to send (Call), since which player they go to takes a look at Home Assistant.
func Control(i int, entity, what string) {
	if s := current.Load(); s != nil {
		s.Control(i, entity, what, time.Now())
	}
}

// Control is Control on this screen. A garage door, a gate or a door is opened only by a second tap, on
// the key or the tile, as a tap on the tile opens it.
func (s *Screen) Control(i int, entity, what string, now time.Time) {
	raw, t, ok := s.tile(i, entity, now)
	if !ok || t.State == "unavailable" {
		return
	}
	call := func(service string, data map[string]string, next string) {
		c := component.Call{Service: service, Data: map[string]string{"entity_id": t.Entity}}
		for k, v := range data {
			c.Data[k] = v
		}
		slog.Info("tessera control", "entity", t.Entity, "service", service)
		component.CallService.Emit(c)
		if next != "" {
			s.hold(i, raw, pending{state: next}, now)
		} else {
			s.drop(i)
		}
	}
	switch d := domain(t.Entity); {
	case d == "climate" && (what == "down" || what == "up"):
		l := level(d, t.State, attrs(t.A))
		if l == nil {
			return
		}
		step := l.Step
		if what == "down" {
			step = -step
		}
		s.setLevel(i, raw, t, snap(l.Value+step, *l), now)
	case d == "climate" && strings.HasPrefix(what, "mode:"):
		mode := strings.TrimPrefix(what, "mode:")
		call("climate.set_hvac_mode", map[string]string{"hvac_mode": mode}, mode)
	case d == "cover" && what == "open":
		if guarded(t, "cover.open_cover") && !s.sure(i, raw, now) {
			return
		}
		call("cover.open_cover", nil, "opening")
	case d == "cover" && what == "close":
		call("cover.close_cover", nil, "closing")
	case d == "cover" && what == "stop":
		call("cover.stop_cover", nil, "")
	}
}

// snap is v on a level's steps, within its range.
func snap(v float64, l Level) float64 {
	v = min(max(v, l.Min), l.Max)
	if l.Step > 0 {
		v = l.Min + math.Round((v-l.Min)/l.Step)*l.Step
	}
	return math.Round(v*100) / 100
}
