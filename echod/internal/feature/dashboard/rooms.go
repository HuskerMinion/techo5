//go:build !dot && !spot

package dashboard

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The Rooms dashboard: every area in Home Assistant with the things in it that are worth a tap or a
// glance - lights, switches, fans, covers, thermostats, players, locks, doors and windows - and the
// room's temperature, the way Home Assistant's own areas dashboard lays a house out. It needs no
// dashboard to have been made, which makes it the one a new device can show at once.

// shown is which domains a room shows, in the order its tiles go.
var shown = []string{"light", "switch", "fan", "cover", "climate", "media_player", "lock", "input_boolean", "binary_sensor"}

// openings is the binary sensors worth a tile: whether something is open.
var openings = map[string]bool{"door": true, "window": true, "garage_door": true, "opening": true, "lock": true}

type plannedRoom struct {
	name, temp, humidity string
	entities             []string
}

// roomsSource is the Rooms dashboard: the rooms are read once a connection, their things followed.
type roomsSource struct {
	plan []plannedRoom
}

func (r *roomsSource) load(ctx context.Context, live *hass.Live) (needs, error) {
	areas, floors, devices, entities, err := live.Registries(ctx)
	if err != nil {
		return needs{}, fmt.Errorf("Home Assistant would not list its rooms.")
	}
	plan, ids := planRooms(areas, floors, devices, entities)
	r.plan = plan
	return needs{entities: ids}, nil
}

// sections is a section a room: its name and climate over its tiles.
func (r *roomsSource) sections(l look) []Section {
	var out []Section
	for _, p := range r.plan {
		head := Block{Heading: p.name, Right: climateOf(l.states[p.temp], l.states[p.humidity])}
		var tiles []Tile
		for _, id := range p.entities {
			e, ok := l.states[id]
			if !ok {
				continue
			}
			if t, keep := tileOf(e, p.name); keep {
				tiles = append(tiles, t)
			}
		}
		if len(tiles) == 0 && head.Right == "" {
			continue
		}
		sec := Section{Blocks: []Block{head}}
		if len(tiles) > 0 {
			sec.Blocks = append(sec.Blocks, Block{Tiles: tiles})
		}
		out = append(out, sec)
	}
	return out
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

// tileOf is how one entity looks as a tile, and whether it is worth one: a binary sensor only
// when it says whether something is open.
func tileOf(e hass.LiveEntity, room string) (Tile, bool) {
	domain, _, _ := strings.Cut(e.ID, ".")
	class, _ := e.Attrs["device_class"].(string)
	if domain == "binary_sensor" && !openings[class] {
		return Tile{}, false
	}
	return describe(e, nameOf(e, room)), true
}

// describe is how any entity looks as a tile: its icon, its name, and its state in words, with the
// tap that suits it. Locks and thermostats get none: a tap is too easy to make by accident for a
// front door, and a thermostat wants more than on and off.
func describe(e hass.LiveEntity, name string) Tile {
	domain, _, _ := strings.Cut(e.ID, ".")
	class, _ := e.Attrs["device_class"].(string)
	t := Tile{Name: name}
	on := e.State == "on" || e.State == "open" || e.State == "opening" || e.State == "playing" ||
		e.State == "unlocked" || e.State == "heat" || e.State == "cool" || e.State == "heat_cool" || e.State == "home"
	t.On = on
	toggle := func(service string) *Action { return &Action{Entity: e.ID, Service: service} }
	switch domain {
	case "light":
		t.Value, t.Tap = onOff(e.State), toggle("light.toggle")
		if on {
			if b, ok := e.Attrs["brightness"].(float64); ok {
				t.Value = fmt.Sprintf("On · %d%%", int(math.Round(b/255*100)))
			}
		}
	case "switch", "input_boolean", "fan", "siren", "automation":
		t.Value, t.Tap = onOff(e.State), toggle(domain+".toggle")
	case "cover":
		t.Value, t.Tap = title(e.State), toggle("cover.toggle")
		if p, ok := e.Attrs["current_position"].(float64); ok && e.State == "open" && p < 100 {
			t.Value = fmt.Sprintf("Open · %d%%", int(p))
		}
	case "climate":
		t.Value = climateValue(e)
	case "media_player":
		t.Value = title(e.State)
		if e.State == "playing" || e.State == "paused" {
			t.Tap = toggle("media_player.media_play_pause")
		}
		t.On = e.State == "playing"
		if title, _ := e.Attrs["media_title"].(string); title != "" && e.State == "playing" {
			t.Value = title
		}
	case "lock":
		t.Value = title(e.State)
		t.On = e.State != "locked"
	case "binary_sensor":
		t.Value = binaryWords(class, e.State == "on")
	case "scene", "script", "button", "input_button":
		t.Value = title(domain)
		service := map[string]string{"scene": "scene.turn_on", "script": "script.turn_on", "button": "button.press", "input_button": "input_button.press"}[domain]
		t.Tap, t.On = toggle(service), false
	case "sensor", "input_number", "number", "counter":
		t.Value = e.State
		if n := number(e.State); n != "" && strings.Contains(e.State, ".") {
			// Two places are more than a glance wants; one is kept where the number is small.
			var f float64
			fmt.Sscanf(e.State, "%g", &f)
			t.Value = trimNumber(f)
		}
		if unit, _ := e.Attrs["unit_of_measurement"].(string); unit != "" {
			t.Value += " " + unit
		}
		t.On = false
	default:
		t.Value = title(e.State)
	}
	t.Adjust = adjustOf(e, domain)
	if e.State == "unavailable" || e.State == "unknown" {
		t.Value, t.Gone, t.On, t.Tap, t.Adjust = title(e.State), true, false, nil, nil
	}
	t.Icon = iconOf(e, domain, class, t.On)
	return t
}

// binaryWords is a binary sensor's state as its kind says it.
func binaryWords(class string, on bool) string {
	words := map[string][2]string{
		"door": {"Open", "Closed"}, "window": {"Open", "Closed"}, "garage_door": {"Open", "Closed"},
		"opening": {"Open", "Closed"}, "lock": {"Unlocked", "Locked"}, "motion": {"Detected", "Clear"},
		"occupancy": {"Detected", "Clear"}, "presence": {"Home", "Away"}, "moisture": {"Wet", "Dry"},
		"smoke": {"Detected", "Clear"}, "battery": {"Low", "Normal"}, "connectivity": {"Connected", "Disconnected"},
		"problem": {"Problem", "OK"}, "plug": {"Plugged in", "Unplugged"}, "power": {"On", "Off"},
	}
	w, ok := words[class]
	if !ok {
		w = [2]string{"On", "Off"}
	}
	if on {
		return w[0]
	}
	return w[1]
}

func trimNumber(f float64) string {
	switch {
	case math.Abs(f) >= 100:
		return fmt.Sprintf("%.0f", f)
	default:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", f), "0"), ".")
	}
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
		case "motion", "occupancy", "presence":
			return pick("motion-sensor", "motion-sensor-off")
		case "moisture":
			return pick("water", "water-off")
		case "smoke":
			return pick("smoke-detector-alert", "smoke-detector")
		case "door", "opening", "":
			return pick("door-open", "door-closed")
		}
		return pick("checkbox-marked-circle", "checkbox-blank-circle-outline")
	case "sensor":
		switch class {
		case "temperature":
			return "thermometer"
		case "humidity":
			return "water-percent"
		case "battery":
			return "battery"
		case "power", "energy":
			return "flash"
		case "illuminance":
			return "brightness-5"
		}
		return "eye"
	case "scene":
		return "palette"
	case "script":
		return "script-text"
	case "automation":
		return "robot"
	case "person":
		return "account"
	case "weather":
		return "weather-partly-cloudy"
	case "button", "input_button":
		return "gesture-tap-button"
	case "input_number", "number", "counter":
		return "ray-vertex"
	case "siren":
		return "bullhorn"
	}
	return "help-circle-outline"
}

// adjustOf is the level a finger sliding along the entity's tile sets, if it has one: a light that
// dims, a cover that stops part way, a thermostat's temperature.
func adjustOf(e hass.LiveEntity, domain string) *Adjust {
	switch domain {
	case "light":
		modes, _ := e.Attrs["supported_color_modes"].([]any)
		dims := len(modes) == 0 && e.Attrs["brightness"] != nil
		for _, m := range modes {
			if m != "onoff" {
				dims = true
			}
		}
		if !dims {
			return nil
		}
		v := 0.0
		if b, ok := e.Attrs["brightness"].(float64); ok && e.State == "on" {
			v = b / 255 * 100
		}
		return &Adjust{Entity: e.ID, Kind: "brightness", Value: v, Min: 0, Max: 100, Step: 1}
	case "cover":
		p, ok := e.Attrs["current_position"].(float64)
		if !ok {
			return nil
		}
		return &Adjust{Entity: e.ID, Kind: "position", Value: p, Min: 0, Max: 100, Step: 1}
	case "climate":
		t, ok := e.Attrs["temperature"].(float64)
		if !ok {
			return nil
		}
		lo, _ := e.Attrs["min_temp"].(float64)
		hi, _ := e.Attrs["max_temp"].(float64)
		step, _ := e.Attrs["target_temp_step"].(float64)
		if hi <= lo {
			lo, hi = t-10, t+10
		}
		if step <= 0 {
			step = 0.5
			if hi > 40 {
				step = 1 // Fahrenheit
			}
		}
		return &Adjust{Entity: e.ID, Kind: "temperature", Value: t, Min: lo, Max: hi, Step: step}
	}
	return nil
}
