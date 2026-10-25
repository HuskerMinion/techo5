package tessera

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// What the screen draws, worked out from the layout in force: each page's tiles placed on the cells of
// the grid, and each tile in words and an icon, the way the Show's drawn dashboard puts its own. Nothing
// here draws; the display does, in its own look (display/tessera.go). The add-on names roles - a size, an
// icon, a background swatch - and the screen chooses how they look.

// View is the layout as the screen shows it.
type View struct {
	Rev           string
	Title         string
	Columns, Rows int
	Home          int
	Pages         []PageView
}

// PageView is one page: its title, whether it is a detail page left out of the swipes and the dots, its
// tiles where they sit, and its top bar.
type PageView struct {
	Title    string
	Excluded bool
	Tiles    []Placed
	Bar      []BarItem
}

// Placed is one tile on a page: the cells it covers, counted from the top left, and what it shows.
type Placed struct {
	I                    int // its place in the layout, for a tap to name it
	Col, Row, Cols, Rows int

	Entity, Name string
	State        string // its state, as Home Assistant names it
	Icon         string // an mdi name
	Value        string // what it is doing, in words: "On · 60%", "21.5 °C"
	On, Gone     bool
	// Reading is a tile whose value is the point of it, a sensor's: drawn large.
	Reading bool
	// Background is the swatch the layout gave the tile (red, orange, yellow, green, mint, blue, purple,
	// pink, gray), empty for none.
	Background string
	// Taps is whether a tap does something; Level is a level a finger sliding along it sets.
	Taps  bool
	Level *Level
	// Detail is a tile whose tap opens its card, the sheet a long press opens, rather than doing what it
	// is for: a thermostat's, and any the layout says so of.
	Detail bool
	// Confirm is a tile a tap has asked to be sure about: a lock a second tap unlocks.
	Confirm bool
	// Days is the forecast a weather tile has room for, from today.
	Days []Day
	// Controls are the rows of controls the add-on says the card draws, in order: setpoint and mode
	// for a thermostat, volume and playback for a player, buttons and position for a cover, brightness
	// for a light, speed for a fan. Only those drawn here are kept.
	Controls []string
	// Modes are a thermostat's modes, for its mode row; Volume is a player's, from 0 to 1, or -1.
	Modes  []string
	Volume float64
	// Picture is a camera's tile that shows its picture (the layout's display live) rather than its
	// icon: Whole for all of the picture rather than the tile filled, Bare for no name on it, Refresh
	// between one snapshot and the next, zero for the screen's own pace.
	Picture, Whole, Bare bool
	Refresh              time.Duration
}

// Day is a day of a forecast: its short name, its weather and its icon, and its high and low.
type Day struct {
	Name, Condition string
	Icon            string // an mdi name
	High, Low       float64
	HasHigh, HasLow bool
}

// Level is a tile's level: what it is now and how far it goes.
type Level struct {
	Kind            string // brightness, position, temperature, value
	Value, Min, Max float64
	Step            float64
}

// BarItem is one item of a page's top bar.
type BarItem struct {
	Kind  string    // clock, date, text or ago; the rest are not drawn
	Text  string    // a text item's words, the unit already on them
	Icon  string    // an mdi name, or empty
	Color string    // six hex digits, empty for the theme's
	At    time.Time // the moment an ago item counts from
}

// grid is a screen's size in pixels and the cells of a page.
type grid struct{ width, height, columns, rows int }

func (g grid) String() string {
	return fmt.Sprintf("%dx%d %dx%d standard", g.width, g.height, g.columns, g.rows)
}

// options is the part of a tile's options read here: its size, icon and background, what a tap does
// (auto, detail, toggle, action or none), and how far a lock's tap goes (lock_only, or the default,
// confirm, which the add-on leaves out).
type options struct {
	Size       string          `json:"size"`
	Icon       json.RawMessage `json:"icon"`
	Background json.RawMessage `json:"background"`
	Tap        string          `json:"tap"`
	Guard      string          `json:"guard"`
	Controls   string          `json:"controls"`
	// How a camera's tile shows its picture: display live (else its icon), fit contain (the whole
	// picture, else it fills the tile), overlay none (no name on it), refresh (seconds between pictures).
	Display string `json:"display"`
	Fit     string `json:"fit"`
	Overlay string `json:"overlay"`
	Refresh int    `json:"refresh"`
}

// lockOnly is the guard of a lock a tap only locks.
const lockOnly = "lock_only"

// tileOptions is a tile's options, decoded.
func tileOptions(t Tile) options {
	var o options
	_ = json.Unmarshal(t.O, &o)
	return o
}

// barWire is a top-bar item as it comes: its kind, words, icon, color and moment.
type barWire struct {
	K string          `json:"k"`
	T string          `json:"t"`
	I json.RawMessage `json:"i"`
	C string          `json:"c"`
	E int64           `json:"e"`
}

// span is the cells a size covers on a grid of cols by rows: one, two side by side, two stacked, two by
// two, the whole page, or columns by rows written out ("3x2"). A size this does not know is one cell.
func span(size string, cols, rows int) (int, int) {
	w, h := 1, 1
	switch size {
	case "", "single":
	case "wide":
		w = 2
	case "tall":
		h = 2
	case "square":
		w, h = 2, 2
	case "full":
		w, h = cols, rows
	default:
		c, r, ok := strings.Cut(size, "x")
		if a, err := strconv.Atoi(c); ok && err == nil && a > 0 {
			if b, err := strconv.Atoi(r); err == nil && b > 0 {
				w, h = a, b
			}
		}
	}
	return min(w, cols), min(h, rows)
}

// view places the tiles of l on a grid of g's cells, unless the layout names a grid of its own. A tile's
// slot counts the cells of every page before its own: slot / cells is the page, and the rest is the row
// and column. A tile of the top bar alone, or a key of another tile, has no slot and is not placed.
func view(l *Layout, g grid, now time.Time, held map[int]pending) View {
	if l == nil {
		return View{Columns: g.columns, Rows: g.rows}
	}
	cols, rows := g.columns, g.rows
	if len(l.Begin.Grid) == 2 && l.Begin.Grid[0] > 0 && l.Begin.Grid[1] > 0 && l.Begin.Grid[0] <= 9 && l.Begin.Grid[1] <= 9 {
		cols, rows = l.Begin.Grid[0], l.Begin.Grid[1]
	}
	v := View{Rev: l.Rev, Title: l.Begin.Title, Columns: cols, Rows: rows, Home: l.Begin.Home}
	for _, p := range l.Pages {
		v.Pages = append(v.Pages, PageView{Title: p.Title, Excluded: p.Excluded, Bar: barItems(p.Items)})
	}
	cells := cols * rows
	for i, t := range l.Tiles {
		if t.Slot == nil || t.In != nil || t.Bar != 0 || *t.Slot < 0 {
			continue
		}
		page, local := *t.Slot/cells, *t.Slot%cells
		if page >= len(v.Pages) {
			continue
		}
		o := tileOptions(t)
		w, h := span(o.Size, cols, rows)
		row, col := local/cols, local%cols
		// A tile running past the right edge or the foot is moved in, as far as it can be.
		col, row = max(min(col, cols-w), 0), max(min(row, rows-h), 0)
		p, holding := held[i]
		holding = holding && p.holds(t, now)
		if holding {
			t = p.over(t)
		}
		pl := describe(t, o)
		if domain(t.Entity) == "camera" && o.Display == "live" {
			pl.Picture, pl.Whole, pl.Bare = true, o.Fit == "contain", o.Overlay == "none"
			pl.Refresh = time.Duration(max(o.Refresh, 0)) * time.Second
		}
		if holding && p.confirm && pl.Taps {
			pl.Confirm, pl.Value = true, "Tap again to unlock"
			if domain(t.Entity) == "cover" {
				pl.Value = "Tap again to open"
			}
		}
		pl.I, pl.Col, pl.Row, pl.Cols, pl.Rows = i, col, row, w, h
		if bg, ok := l.Backgrounds[i]; ok {
			pl.Background = swatch(bg)
		}
		v.Pages[page].Tiles = append(v.Pages[page].Tiles, pl)
	}
	return v
}

// Place is l placed on a grid of columns by rows, as the screen draws it: for a preview, and for tests
// of what draws it.
func Place(l *Layout, columns, rows int) View {
	return view(l, grid{columns: columns, rows: rows}, time.Now(), nil)
}

// barItems is a page's top bar, the items this draws.
func barItems(raw json.RawMessage) []BarItem {
	var wire []barWire
	if json.Unmarshal(raw, &wire) != nil {
		return nil
	}
	var out []BarItem
	for _, w := range wire {
		switch w.K {
		case "clock", "analog", "date", "text", "ago":
		default:
			continue // Wi-Fi, battery, link and plugins: the screen's own status, or nothing here yet
		}
		it := BarItem{Kind: w.K, Text: w.T, Color: w.C}
		if w.K == "analog" {
			it.Kind = "clock"
		}
		it.Icon, _ = iconName(w.I)
		if w.E > 0 {
			it.At = time.Unix(w.E, 0)
		}
		out = append(out, it)
	}
	return out
}

// swatch is a background's name, empty for none and for the add-on's own choice.
func swatch(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil || s == "auto" || s == "none" {
		return ""
	}
	return s
}

// iconName is an icon as the add-on sends it - its place in the icon font, as a number or as hex digits,
// or its name - as an mdi name.
func iconName(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		return mdi.Name(rune(n))
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || s == "" || s == "auto" || s == "none" {
		return "", false
	}
	if v, err := strconv.ParseUint(strings.TrimPrefix(strings.ToUpper(s), "U+"), 16, 32); err == nil && v >= 0xF0000 {
		return mdi.Name(rune(v))
	}
	if _, ok := mdi.Rune(s); ok {
		return strings.TrimPrefix(s, "mdi:"), true
	}
	return "", false
}

// attrs is a tile's attributes, decoded.
func attrs(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}

func num(a map[string]any, key string) (float64, bool) {
	v, ok := a[key].(float64)
	return v, ok && !math.IsNaN(v)
}

func str(a map[string]any, key string) string {
	s, _ := a[key].(string)
	return s
}

// domain is an entity's domain: "light" for light.kitchen.
func domain(entity string) string {
	d, _, _ := strings.Cut(entity, ".")
	return d
}

// describe is a tile in words and an icon, and what a finger can do to it.
func describe(t Tile, o options) Placed {
	a := attrs(t.A)
	if domain(t.Entity) == "media_player" && str(a, "media_artist") == "" {
		// The artist comes with the extras, as the media card's does.
		if artist := str(attrs(t.X), "artist"); artist != "" {
			a["media_artist"] = artist
		}
	}
	d := domain(t.Entity)
	p := Placed{Entity: t.Entity, Name: t.Name, Background: swatch(o.Background)}
	if p.Name == "" {
		p.Name = str(a, "friendly_name")
	}
	if p.Name == "" {
		p.Name = t.Entity
	}
	state := t.State
	p.State = state
	p.Gone = state == "unavailable"
	p.On = lit(d, state)
	p.Value = words(d, state, a)
	p.Reading = d == "sensor" || d == "input_number" || d == "number" || d == "weather"
	_, p.Taps = tapService(t.Entity, state)
	switch {
	case o.Tap == "none":
		p.Taps = false
	case o.Tap == "detail":
		// Its card, where the screen has one; nothing where it has none.
		p.Taps = hasSheet(d) && !p.Gone
		p.Detail = p.Taps
	case (o.Tap == "" || o.Tap == "auto") && d == "climate" && !p.Gone:
		p.Taps, p.Detail = true, true
	case d == "lock" && state == "locked" && o.Guard == lockOnly:
		p.Taps = false
	}
	if !p.Gone {
		p.Level = level(d, state, a)
	}
	if d == "weather" {
		p.Days = days(t.X)
	}
	if !p.Gone {
		p.Controls = controlRows(d, o.Controls)
	}
	p.Volume = -1
	if v, ok := num(a, "volume_level"); ok {
		p.Volume = v
	}
	if m, ok := a["hvac_modes"].([]any); ok {
		for _, x := range m {
			if s, ok := x.(string); ok {
				p.Modes = append(p.Modes, s)
			}
		}
	}

	// The icon: the one the state message worked out, else the tile's own, else its domain's.
	name, ok := iconName(t.Icon)
	if !ok {
		name, ok = iconName(o.Icon)
	}
	if !ok {
		name = defaultIcon(d, state, a)
	}
	p.Icon = name
	return p
}

// lit is whether a tile in this state is drawn lit, as Home Assistant colors one that is on.
func lit(domain, state string) bool {
	switch state {
	case "on", "open", "opening", "closing", "playing", "unlocked", "unlocking", "home", "cleaning", "active",
		"heat", "cool", "heat_cool", "auto", "dry", "fan_only", "triggered", "armed_home", "armed_away", "armed_night":
		return domain != "sensor" && domain != "person" || state == "home"
	}
	return false
}

// words is what a tile says it is doing.
func words(domain, state string, a map[string]any) string {
	// What a scene, a script or a button says is what a tap does: its state is when it last ran.
	if state != "unavailable" {
		switch {
		case domain == "script" && state == "on":
			return "Running"
		case domain == "scene" || domain == "script":
			return "Tap to run"
		case domain == "button" || domain == "input_button":
			return "Tap to press"
		}
	}
	switch state {
	case "unavailable":
		return "Unavailable"
	case "unknown", "":
		return "—"
	}
	switch domain {
	case "sensor", "input_number", "number":
		return withUnit(state, str(a, "unit_of_measurement"))
	case "light":
		if b, ok := num(a, "brightness"); ok && state == "on" {
			return fmt.Sprintf("On · %d%%", int(b/255*100+0.5))
		}
	case "cover":
		if p, ok := num(a, "current_position"); ok && state == "open" {
			return fmt.Sprintf("Open · %d%%", int(p+0.5))
		}
	case "fan":
		if p, ok := num(a, "percentage"); ok && state == "on" {
			return fmt.Sprintf("On · %d%%", int(p+0.5))
		}
	case "binary_sensor":
		return binaryWords(str(a, "device_class"), state == "on")
	case "climate":
		w := human(state)
		if t, ok := num(a, "temperature"); ok && state != "off" {
			w += " · " + trimNumber(t) + "°"
		}
		if c, ok := num(a, "current_temperature"); ok {
			w = trimNumber(c) + "° · " + w
		}
		return w
	case "media_player":
		if title := str(a, "media_title"); title != "" && (state == "playing" || state == "paused") {
			if artist := str(a, "media_artist"); artist != "" {
				return title + " · " + artist
			}
			return title
		}
	case "weather":
		if t, ok := num(a, "temperature"); ok {
			return trimNumber(t) + str(a, "temperature_unit") + " · " + weatherWords(state)
		}
		return weatherWords(state)
	case "person", "device_tracker":
		if state == "not_home" {
			return "Away"
		}
	}
	return human(state)
}

// binaryWords is an on or off sensor in the words its class has for them.
func binaryWords(class string, on bool) string {
	pick := func(yes, no string) string {
		if on {
			return yes
		}
		return no
	}
	switch class {
	case "door", "window", "garage_door", "opening":
		return pick("Open", "Closed")
	case "motion", "occupancy", "presence", "sound", "vibration":
		return pick("Detected", "Clear")
	case "moisture":
		return pick("Wet", "Dry")
	case "lock":
		return pick("Unlocked", "Locked")
	case "battery":
		return pick("Low", "Normal")
	case "connectivity":
		return pick("Connected", "Disconnected")
	case "problem":
		return pick("Problem", "OK")
	case "smoke", "gas", "carbon_monoxide", "safety":
		return pick("Detected", "Clear")
	}
	return pick("On", "Off")
}

// weatherWords is a weather condition in words.
func weatherWords(state string) string {
	switch state {
	case "partlycloudy":
		return "Partly cloudy"
	case "clear-night":
		return "Clear"
	case "lightning-rainy":
		return "Storms"
	case "snowy-rainy":
		return "Sleet"
	case "windy-variant":
		return "Windy"
	}
	return human(state)
}

// human is a state as words: "not_home" as "Not home". The first letter is a whole one, however many
// bytes it takes.
func human(state string) string {
	s := strings.ReplaceAll(state, "_", " ")
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// withUnit is a reading and its unit, spaced as Home Assistant spaces them: none before a percent sign
// or a bare degree.
func withUnit(v, unit string) string {
	switch unit {
	case "":
		return v
	case "%", "°":
		return v + unit
	}
	return v + " " + unit
}

// trimNumber is a number without a fraction it does not have: 21 for 21.0, 21.5 for 21.5.
func trimNumber(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

// level is the level a finger sliding along the tile sets, if it has one.
func level(domain, state string, a map[string]any) *Level {
	switch domain {
	case "light":
		modes, _ := a["supported_color_modes"].([]any)
		dims := false
		for _, m := range modes {
			dims = dims || m != "onoff"
		}
		b, has := num(a, "brightness")
		if !dims && !has {
			return nil
		}
		v := 0.0
		if state == "on" {
			v = b / 255 * 100
		}
		return &Level{Kind: "brightness", Value: v, Min: 0, Max: 100, Step: 1}
	case "cover":
		if p, ok := num(a, "current_position"); ok {
			return &Level{Kind: "position", Value: p, Min: 0, Max: 100, Step: 1}
		}
	case "climate":
		t, ok := num(a, "temperature")
		if !ok || state == "off" {
			return nil
		}
		lo, hi, step := 7.0, 35.0, 0.5
		if v, ok := num(a, "min_temp"); ok {
			lo = v
		}
		if v, ok := num(a, "max_temp"); ok {
			hi = v
		}
		if v, ok := num(a, "target_temp_step"); ok && v > 0 {
			step = v
		}
		if hi <= lo {
			return nil // no range to slide along
		}
		return &Level{Kind: "temperature", Value: t, Min: lo, Max: hi, Step: step}
	case "fan":
		p, ok := num(a, "percentage")
		if !ok {
			return nil
		}
		if state != "on" {
			p = 0
		}
		step, _ := num(a, "percentage_step")
		return &Level{Kind: "percentage", Value: p, Min: 0, Max: 100, Step: step}
	case "input_number", "number":
		v, err := strconv.ParseFloat(state, 64)
		lo, okLo := num(a, "min")
		hi, okHi := num(a, "max")
		if err != nil || !okLo || !okHi || hi <= lo {
			return nil
		}
		step, _ := num(a, "step")
		return &Level{Kind: "value", Value: v, Min: lo, Max: hi, Step: step}
	}
	return nil
}

// withLevel is attributes with a level a finger set, to show it until Home Assistant says.
func withLevel(raw json.RawMessage, entity string, v float64) json.RawMessage {
	a := attrs(raw)
	switch domain(entity) {
	case "light":
		a["brightness"] = v / 100 * 255
	case "cover":
		a["current_position"] = v
	case "climate":
		a["temperature"] = v
	case "fan":
		a["percentage"] = v
	case "media_player":
		a["volume_level"] = v / 100
	}
	b, err := json.Marshal(a)
	if err != nil {
		return raw
	}
	return b
}

// controlRows are the rows a card's controls (its option, as the add-on resolved it for the card's
// size) come to, of those drawn here: a pair is its two rows, and a blind's slats are left out.
func controlRows(domain, key string) []string {
	var rows []string
	switch key {
	case "setpoint_mode":
		rows = []string{"setpoint", "mode"}
	case "volume_playback":
		rows = []string{"volume", "playback"}
	case "buttons_tilt":
		rows = []string{"buttons"}
	case "position_tilt":
		rows = []string{"position"}
	default:
		rows = []string{key}
	}
	drawn := map[string][]string{
		"climate":      {"setpoint", "mode"},
		"media_player": {"volume", "playback"},
		"cover":        {"buttons", "position"},
		"light":        {"brightness"},
		"fan":          {"speed"},
	}[domain]
	var out []string
	for _, r := range rows {
		for _, d := range drawn {
			if r == d {
				out = append(out, r)
			}
		}
	}
	return out
}

// hasSheet is whether the screen has a card for an entity of this domain, the sheet a long press opens:
// a light's colors, a thermostat's controls, a player's music.
func hasSheet(domain string) bool {
	return domain == "light" || domain == "climate" || domain == "media_player"
}

// dayWire is a day of a weather tile's forecast as the extras carry it: its short name, its condition,
// its high and its low.
type dayWire struct {
	D string   `json:"d"`
	C string   `json:"c"`
	H *float64 `json:"h"`
	L *float64 `json:"l"`
}

// days is the forecast in a weather tile's extras, as many days as came.
func days(raw json.RawMessage) []Day {
	var x struct {
		Days []dayWire `json:"days"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &x) != nil {
		return nil
	}
	var out []Day
	for _, w := range x.Days {
		d := Day{Name: w.D, Condition: w.C, Icon: weatherIcon(w.C)}
		if w.H != nil {
			d.High, d.HasHigh = *w.H, true
		}
		if w.L != nil {
			d.Low, d.HasLow = *w.L, true
		}
		out = append(out, d)
	}
	return out
}

// defaultIcon is a tile's icon when the layout names none: its domain's, by its class and state.
func defaultIcon(domain, state string, a map[string]any) string {
	on := state == "on"
	pick := func(yes, no string) string {
		if on {
			return yes
		}
		return no
	}
	switch domain {
	case "light":
		return pick("lightbulb", "lightbulb-off")
	case "switch", "input_boolean":
		return pick("toggle-switch", "toggle-switch-off")
	case "fan":
		return pick("fan", "fan-off")
	case "sensor":
		switch str(a, "device_class") {
		case "temperature":
			return "thermometer"
		case "humidity", "moisture":
			return "water-percent"
		case "battery":
			return "battery"
		case "power", "energy", "current", "voltage":
			return "flash"
		case "illuminance":
			return "brightness-5"
		case "pressure":
			return "gauge"
		}
		return "eye"
	case "binary_sensor":
		switch str(a, "device_class") {
		case "door", "garage_door", "opening":
			return pick("door-open", "door-closed")
		case "motion", "occupancy", "presence":
			return "motion-sensor"
		case "moisture":
			return "water-alert"
		}
		return pick("checkbox-marked-circle", "checkbox-blank-circle-outline")
	case "cover":
		if state == "closed" {
			return "window-shutter"
		}
		return "window-shutter-open"
	case "lock":
		if state == "locked" {
			return "lock"
		}
		return "lock-open-variant"
	case "climate":
		return "thermostat"
	case "media_player":
		return "speaker"
	case "weather":
		return weatherIcon(state)
	case "person", "device_tracker":
		return "account"
	case "scene":
		return "palette"
	case "script":
		return "script-text"
	case "button", "input_button":
		return "gesture-tap-button"
	case "automation":
		return "robot"
	case "input_number", "number":
		return "ray-vertex"
	case "select", "input_select":
		return "format-list-bulleted"
	case "camera":
		return "video"
	case "sun":
		return "white-balance-sunny"
	case "vacuum":
		return "robot-vacuum"
	case "timer":
		return "timer-outline"
	case "humidifier":
		return "air-humidifier"
	case "alarm_control_panel":
		return "shield-home"
	case "calendar":
		return "calendar"
	}
	return "shape"
}

// weatherIcon is a weather condition's icon.
func weatherIcon(state string) string {
	switch state {
	case "sunny":
		return "weather-sunny"
	case "clear-night":
		return "weather-night"
	case "partlycloudy":
		return "weather-partly-cloudy"
	case "cloudy":
		return "weather-cloudy"
	case "rainy":
		return "weather-rainy"
	case "pouring":
		return "weather-pouring"
	case "snowy":
		return "weather-snowy"
	case "snowy-rainy":
		return "weather-snowy-rainy"
	case "fog":
		return "weather-fog"
	case "hail":
		return "weather-hail"
	case "lightning", "lightning-rainy":
		return "weather-lightning-rainy"
	case "windy", "windy-variant":
		return "weather-windy"
	}
	return "weather-partly-cloudy"
}
