//go:build !dot && !spot

package dashboard

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The Rooms dashboard: every area in Home Assistant with the things in it that are worth a tap or a
// glance - lights, switches, fans, covers, thermostats, players, locks, doors and windows - and the
// room's temperature, the way Home Assistant's own areas dashboard lays a house out. It needs no
// dashboard to have been made, which makes it the one a new device can show at once.

// Tile is one thing in a room, as the page draws it.
type Tile struct {
	Entity string
	Name   string
	Icon   string // an mdi name
	Value  string // what it is doing, in words: "On · 60%", "Closed", "72°"
	On     bool   // lit, open, playing: drawn in the accent color
	Tap    bool   // a tap does something
	Busy   bool   // tapped, and Home Assistant has not said it changed yet
	Gone   bool   // unavailable
}

// Room is an area and its tiles.
type Room struct {
	Name    string
	Climate string // the room's temperature and humidity, when it has a sensor for them
	Tiles   []Tile
}

// Drawn is the Rooms dashboard as it stands.
type Drawn struct {
	Rooms   []Room
	Problem string
	Version uint64
}

// shown is which domains a room shows, in the order its tiles go.
var shown = []string{"light", "switch", "fan", "cover", "climate", "media_player", "lock", "input_boolean", "binary_sensor"}

// openings is the binary sensors worth a tile: whether something is open, or somebody is there.
var openings = map[string]bool{"door": true, "window": true, "garage_door": true, "opening": true, "lock": true}

// rooms is the live session behind the page: the rooms, and the entities in them as they change.
type rooms struct {
	f *Feature

	mu      sync.Mutex
	live    *hass.Live
	stopped bool
	plan    []plannedRoom
	states  map[string]hass.LiveEntity
	busy    map[string]time.Time
	view    Drawn
}

type plannedRoom struct {
	name, temp, humidity string
	entities             []string
}

// Drawn is the Rooms dashboard, connecting to Home Assistant if it is not already. For the page
// that is up; CloseDrawn ends it.
func (f *Feature) Drawn() Drawn {
	f.mu.Lock()
	r := f.rooms
	if r == nil {
		r = &rooms{f: f, states: map[string]hass.LiveEntity{}, busy: map[string]time.Time{}}
		f.rooms = r
		go r.run()
	}
	f.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.view
}

// CloseDrawn ends the Rooms session, if there is one.
func (f *Feature) CloseDrawn() {
	f.mu.Lock()
	r := f.rooms
	f.rooms = nil
	f.mu.Unlock()
	if r != nil {
		r.close()
	}
}

// TapTile does what a tap on a tile means: lights and switches toggle, covers open or close,
// players play or pause. Locks and thermostats are left to Home Assistant's own screens; a tap is
// too easy to make by accident for a front door.
func (f *Feature) TapTile(entity string) {
	f.mu.Lock()
	r := f.rooms
	f.mu.Unlock()
	if r == nil {
		return
	}
	domain, _, _ := strings.Cut(entity, ".")
	service := map[string][2]string{
		"light": {"light", "toggle"}, "switch": {"switch", "toggle"}, "fan": {"fan", "toggle"},
		"input_boolean": {"input_boolean", "toggle"}, "cover": {"cover", "toggle"},
		"media_player": {"media_player", "media_play_pause"},
	}[domain]
	if service[0] == "" {
		return
	}
	r.mu.Lock()
	live := r.live
	r.busy[entity] = time.Now()
	r.mu.Unlock()
	r.publish()
	if live == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := live.CallService(ctx, service[0], service[1], map[string]any{"entity_id": entity}); err != nil {
			slog.Warn("dashboard: tap failed", "entity", entity, "err", err)
			r.mu.Lock()
			delete(r.busy, entity)
			r.mu.Unlock()
			r.publish()
		}
	}()
}

func (r *rooms) close() {
	r.mu.Lock()
	r.stopped = true
	live := r.live
	r.mu.Unlock()
	if live != nil {
		live.Close()
	}
}

func (r *rooms) problem(text string) {
	r.mu.Lock()
	r.view.Problem = text
	r.view.Version++
	r.mu.Unlock()
	r.f.Changed.Emit(struct{}{})
}

// run keeps a session going until closed.
func (r *rooms) run() {
	wait := time.Second
	for {
		r.mu.Lock()
		stopped := r.stopped
		r.mu.Unlock()
		if stopped {
			return
		}
		if err := r.once(); err != nil {
			slog.Info("dashboard rooms", "err", err)
		}
		r.mu.Lock()
		stopped = r.stopped
		r.mu.Unlock()
		if stopped {
			return
		}
		time.Sleep(wait)
		wait = min(wait*2, 30*time.Second)
	}
}

func (r *rooms) once() error {
	if !hass.Get().Ready() {
		r.problem("The Rooms dashboard needs Home Assistant's address and a token (the hass action).")
		return fmt.Errorf("no home assistant access")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live, err := hass.Get().OpenLive(ctx)
	if err != nil {
		r.problem("Can't reach Home Assistant.")
		return err
	}
	defer live.Close()
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return nil
	}
	r.live = live
	r.mu.Unlock()

	cctx, ccancel := context.WithTimeout(ctx, 30*time.Second)
	areas, floors, devices, entities, err := live.Registries(cctx)
	ccancel()
	if err != nil {
		r.problem("Home Assistant would not list its rooms.")
		return err
	}
	plan, ids := planRooms(areas, floors, devices, entities)
	r.mu.Lock()
	r.plan = plan
	r.view.Problem = ""
	r.mu.Unlock()

	fctx, fcancel := context.WithTimeout(ctx, 30*time.Second)
	err = live.FollowEntities(fctx, ids, func(e hass.LiveEntity) {
		r.mu.Lock()
		r.states[e.ID] = e
		delete(r.busy, e.ID)
		r.mu.Unlock()
		r.publish()
	}, func(id string) {
		r.mu.Lock()
		delete(r.states, id)
		r.mu.Unlock()
		r.publish()
	})
	fcancel()
	if err != nil {
		return err
	}
	slog.Info("dashboard rooms following", "rooms", len(plan), "entities", len(ids))
	<-live.Done()
	return live.Err()
}

// planRooms decides which entities go in which room: an entity's own area, or else its device's.
// Rooms go by floor, top floor first as Home Assistant lists them, then by name.
func planRooms(areas []hass.Area, floors []hass.Floor, devices []hass.Device, entities []hass.Registered) ([]plannedRoom, []string) {
	deviceArea := map[string]string{}
	for _, d := range devices {
		deviceArea[d.ID] = d.Area
	}
	level := map[string]int{}
	for _, f := range floors {
		if f.Level != nil {
			level[f.ID] = *f.Level
		}
	}
	inArea := map[string][]string{}
	for _, e := range entities {
		if e.Hidden || e.Category != nil {
			continue
		}
		domain, _, _ := strings.Cut(e.ID, ".")
		if !slices.Contains(shown, domain) {
			continue
		}
		area := e.Area
		if area == "" {
			area = deviceArea[e.Device]
		}
		if area != "" {
			inArea[area] = append(inArea[area], e.ID)
		}
	}
	slices.SortFunc(areas, func(a, b hass.Area) int {
		if c := cmp.Compare(level[b.Floor], level[a.Floor]); c != 0 {
			return c
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	var plan []plannedRoom
	var ids []string
	for _, a := range areas {
		ents := inArea[a.ID]
		if len(ents) == 0 && a.Temp == "" {
			continue
		}
		slices.SortFunc(ents, func(x, y string) int {
			dx, _, _ := strings.Cut(x, ".")
			dy, _, _ := strings.Cut(y, ".")
			if c := cmp.Compare(slices.Index(shown, dx), slices.Index(shown, dy)); c != 0 {
				return c
			}
			return cmp.Compare(x, y)
		})
		plan = append(plan, plannedRoom{name: a.Name, temp: a.Temp, humidity: a.Humidity, entities: ents})
		ids = append(ids, ents...)
		for _, extra := range []string{a.Temp, a.Humidity} {
			if extra != "" {
				ids = append(ids, extra)
			}
		}
	}
	return plan, ids
}

// publish rebuilds the page's view from the states, and says there is something new to draw.
func (r *rooms) publish() {
	r.mu.Lock()
	var out []Room
	for _, p := range r.plan {
		room := Room{Name: p.name, Climate: climateOf(r.states[p.temp], r.states[p.humidity])}
		for _, id := range p.entities {
			e, ok := r.states[id]
			if !ok {
				continue
			}
			t, keep := tileOf(e, p.name)
			if !keep {
				continue
			}
			if at, busy := r.busy[id]; busy && time.Since(at) < 10*time.Second {
				t.Busy = true
			}
			room.Tiles = append(room.Tiles, t)
		}
		if len(room.Tiles) > 0 || room.Climate != "" {
			out = append(out, room)
		}
	}
	r.view.Rooms = out
	r.view.Version++
	r.mu.Unlock()
	r.f.Changed.Emit(struct{}{})
}

// tileOf is how one entity looks as a tile, and whether it is worth one: a binary sensor only
// when it says whether something is open.
func tileOf(e hass.LiveEntity, room string) (Tile, bool) {
	domain, _, _ := strings.Cut(e.ID, ".")
	class, _ := e.Attrs["device_class"].(string)
	if domain == "binary_sensor" && !openings[class] {
		return Tile{}, false
	}
	t := Tile{Entity: e.ID, Name: nameOf(e, room)}
	on := e.State == "on" || e.State == "open" || e.State == "opening" || e.State == "playing" ||
		e.State == "unlocked" || e.State == "heat" || e.State == "cool" || e.State == "heat_cool"
	t.On = on
	switch domain {
	case "light":
		t.Value, t.Tap = onOff(e.State), true
		if on {
			if b, ok := e.Attrs["brightness"].(float64); ok {
				t.Value = fmt.Sprintf("On · %d%%", int(math.Round(b/255*100)))
			}
		}
	case "switch", "input_boolean", "fan":
		t.Value, t.Tap = onOff(e.State), true
	case "cover":
		t.Value, t.Tap = title(e.State), true
		if p, ok := e.Attrs["current_position"].(float64); ok && e.State == "open" && p < 100 {
			t.Value = fmt.Sprintf("Open · %d%%", int(p))
		}
	case "climate":
		t.Value = climateValue(e)
	case "media_player":
		t.Value, t.Tap = title(e.State), e.State == "playing" || e.State == "paused"
		t.On = e.State == "playing"
		if title, _ := e.Attrs["media_title"].(string); title != "" && e.State == "playing" {
			t.Value = title
		}
	case "lock":
		t.Value = title(e.State)
		t.On = e.State != "locked"
	case "binary_sensor":
		t.Value = "Closed"
		if e.State == "on" {
			t.Value = "Open"
		}
	}
	if e.State == "unavailable" || e.State == "unknown" {
		t.Value, t.Gone, t.On, t.Tap = "Unavailable", true, false, false
	}
	t.Icon = iconOf(e, domain, class, t.On)
	return t, true
}

// nameOf is the entity's name without the room's in front of it: in the Kitchen, "Kitchen ceiling
// light" is the ceiling light.
func nameOf(e hass.LiveEntity, room string) string {
	name, _ := e.Attrs["friendly_name"].(string)
	if name == "" {
		_, name, _ = strings.Cut(e.ID, ".")
	}
	if len(name) > len(room)+1 && strings.EqualFold(name[:len(room)], room) {
		if rest := strings.TrimLeft(name[len(room):], " -:"); rest != "" {
			name = strings.ToUpper(rest[:1]) + rest[1:]
		}
	}
	return name
}

func onOff(s string) string {
	if s == "on" {
		return "On"
	}
	return "Off"
}

func title(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

func climateValue(e hass.LiveEntity) string {
	unit := "°"
	cur, hasCur := e.Attrs["current_temperature"].(float64)
	target, hasTarget := e.Attrs["temperature"].(float64)
	var parts []string
	if hasCur {
		parts = append(parts, fmt.Sprintf("%.0f%s", cur, unit))
	}
	switch {
	case e.State == "off":
		parts = append(parts, "Off")
	case hasTarget:
		parts = append(parts, fmt.Sprintf("%s to %.0f%s", title(e.State), target, unit))
	default:
		parts = append(parts, title(e.State))
	}
	return strings.Join(parts, " · ")
}

// climateOf is a room's temperature and humidity, as its header shows them.
func climateOf(temp, humidity hass.LiveEntity) string {
	var parts []string
	if v := number(temp.State); v != "" {
		parts = append(parts, v+"°")
	}
	if v := number(humidity.State); v != "" {
		parts = append(parts, v+"%")
	}
	return strings.Join(parts, "  ")
}

func number(s string) string {
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err != nil {
		return ""
	}
	return fmt.Sprintf("%.0f", f)
}

// iconOf is the entity's own icon, or the one Home Assistant would pick for its kind and state.
func iconOf(e hass.LiveEntity, domain, class string, on bool) string {
	if icon, _ := e.Attrs["icon"].(string); icon != "" {
		return icon
	}
	pick := func(onIcon, offIcon string) string {
		if on {
			return onIcon
		}
		return offIcon
	}
	switch domain {
	case "light":
		return pick("lightbulb", "lightbulb-outline")
	case "switch", "input_boolean":
		if class == "outlet" {
			return pick("power-plug", "power-plug-off")
		}
		return pick("toggle-switch", "toggle-switch-off-outline")
	case "fan":
		return pick("fan", "fan-off")
	case "cover":
		switch class {
		case "garage":
			return pick("garage-open", "garage")
		case "door", "gate":
			return pick("door-open", "door-closed")
		case "blind", "shade", "curtain":
			return pick("blinds-open", "blinds")
		}
		return pick("window-shutter-open", "window-shutter")
	case "climate":
		return "thermostat"
	case "media_player":
		if class == "tv" {
			return "television"
		}
		return pick("speaker-play", "speaker")
	case "lock":
		return pick("lock-open-variant", "lock")
	case "binary_sensor":
		switch class {
		case "window":
			return pick("window-open", "window-closed")
		case "garage_door":
			return pick("garage-open", "garage")
		}
		return pick("door-open", "door-closed")
	}
	return "help-circle-outline"
}
