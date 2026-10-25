package tessera

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// placedTile is a tile at slot, of size, for the layouts of these tests.
func placedTile(i, slot int, size, entity, state, attrs string) Tile {
	o := `{}`
	if size != "" {
		o = `{"size":"` + size + `"}`
	}
	return Tile{I: i, Slot: &slot, Entity: entity, Name: entity, State: state, A: json.RawMessage(attrs), O: json.RawMessage(o)}
}

func TestTilesArePlacedPageByPage(t *testing.T) {
	in := 0
	l := &Layout{
		Rev:   rev1,
		Begin: Begin{Pages: 2, Tiles: 6, Home: 0},
		Pages: []Page{{P: 0, Title: "Home"}, {P: 1, Title: "Lights"}},
		Tiles: []Tile{
			placedTile(0, 0, "", "light.a", "on", `{}`),
			placedTile(1, 2, "wide", "sensor.b", "1", `{}`),
			placedTile(2, 7, "wide", "switch.c", "off", `{}`), // runs past the right edge: moved in
			placedTile(3, 8, "full", "climate.d", "heat", `{}`),
			placedTile(4, 20, "", "light.e", "on", `{}`), // a page that is not there
			{I: 5, Bar: 1, Entity: "sensor.f"},
		},
	}
	l.Tiles = append(l.Tiles, Tile{I: 6, In: &in, Entity: "light.g"})
	v := view(l, grid{columns: 4, rows: 2}, time.Now(), nil)
	if len(v.Pages) != 2 || v.Columns != 4 || v.Rows != 2 {
		t.Fatalf("view = %d pages, %dx%d", len(v.Pages), v.Columns, v.Rows)
	}
	type at struct{ i, col, row, cols, rows int }
	var home, lights []at
	for _, p := range v.Pages[0].Tiles {
		home = append(home, at{p.I, p.Col, p.Row, p.Cols, p.Rows})
	}
	for _, p := range v.Pages[1].Tiles {
		lights = append(lights, at{p.I, p.Col, p.Row, p.Cols, p.Rows})
	}
	wantHome := []at{{0, 0, 0, 1, 1}, {1, 2, 0, 2, 1}, {2, 2, 1, 2, 1}}
	wantLights := []at{{3, 0, 0, 4, 2}}
	if !equalAt(home, wantHome) || !equalAt(lights, wantLights) {
		t.Errorf("home %v, lights %v; want %v, %v", home, lights, wantHome, wantLights)
	}
}

func equalAt[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestALayoutsOwnGridIsUsed(t *testing.T) {
	l := &Layout{Begin: Begin{Pages: 1, Grid: []int{3, 3}}, Pages: []Page{{}},
		Tiles: []Tile{placedTile(0, 4, "", "light.a", "on", `{}`)}}
	v := view(l, grid{columns: 4, rows: 2}, time.Now(), nil)
	if p := v.Pages[0].Tiles[0]; v.Columns != 3 || p.Col != 1 || p.Row != 1 {
		t.Errorf("slot 4 on a 3x3 grid at %d,%d of %dx%d", p.Col, p.Row, v.Columns, v.Rows)
	}
}

func TestSpans(t *testing.T) {
	for size, want := range map[string][2]int{
		"": {1, 1}, "single": {1, 1}, "wide": {2, 1}, "tall": {1, 2}, "square": {2, 2}, "full": {4, 2},
		"3x2": {3, 2}, "9x9": {4, 2}, "bogus": {1, 1},
	} {
		if w, h := span(size, 4, 2); w != want[0] || h != want[1] {
			t.Errorf("span(%q) = %dx%d, want %dx%d", size, w, h, want[0], want[1])
		}
	}
}

func TestTilesInWords(t *testing.T) {
	kettle, _ := mdi.Rune("kettle")
	for _, c := range []struct {
		entity, state, attrs, options string
		value, icon                   string
		on, gone, taps, level         bool
	}{
		{"light.a", "on", `{"brightness":127.5}`, `{}`, "On · 50%", "lightbulb", true, false, true, true},
		{"light.a", "off", `{"supported_color_modes":["onoff"]}`, `{}`, "Off", "lightbulb-off", false, false, true, false},
		{"switch.k", "off", `{}`, `{"icon":` + itoa(int(kettle)) + `}`, "Off", "kettle", false, false, true, false},
		{"switch.k", "off", `{}`, `{"icon":"F0335"}`, "Off", "lightbulb", false, false, true, false},
		{"sensor.t", "21.5", `{"unit_of_measurement":"°C","device_class":"temperature"}`, `{}`, "21.5 °C", "thermometer", false, false, false, false},
		{"sensor.h", "48", `{"unit_of_measurement":"%"}`, `{}`, "48%", "eye", false, false, false, false},
		{"binary_sensor.d", "on", `{"device_class":"door"}`, `{}`, "Open", "door-open", true, false, false, false},
		{"cover.g", "closed", `{"current_position":0}`, `{}`, "Closed", "window-shutter", false, false, true, true},
		{"lock.b", "locked", `{}`, `{}`, "Locked", "lock", false, false, true, false},
		{"lock.b", "locked", `{}`, `{"guard":"lock_only"}`, "Locked", "lock", false, false, false, false},
		{"lock.b", "unlocked", `{}`, `{}`, "Unlocked", "lock-open-variant", true, false, true, false},
		{"climate.l", "heat", `{"temperature":21,"current_temperature":20.5}`, `{}`, "20.5° · Heat · 21°", "thermostat", true, false, true, true},
		{"fan.f", "on", `{"percentage":50}`, `{}`, "On · 50%", "fan", true, false, true, true},
		{"scene.m", "2026-10-19T17:00:00+00:00", `{}`, `{}`, "Tap to run", "palette", false, false, true, false},
		{"button.p", "unknown", `{}`, `{}`, "Tap to press", "gesture-tap-button", false, false, true, false},
		{"light.p", "unavailable", `{}`, `{}`, "Unavailable", "lightbulb-off", false, true, false, false},
		{"sensor.x", "unknown", `{}`, `{}`, "—", "eye", false, false, false, false},
	} {
		var o options
		_ = json.Unmarshal([]byte(c.options), &o)
		p := describe(Tile{Entity: c.entity, Name: "N", State: c.state, A: json.RawMessage(c.attrs)}, o)
		if p.Value != c.value || p.Icon != c.icon || p.On != c.on || p.Gone != c.gone || p.Taps != c.taps || (p.Level != nil) != c.level {
			t.Errorf("%s %s: %q %q on=%v gone=%v taps=%v level=%v; want %q %q on=%v gone=%v taps=%v level=%v",
				c.entity, c.state, p.Value, p.Icon, p.On, p.Gone, p.Taps, p.Level != nil, c.value, c.icon, c.on, c.gone, c.taps, c.level)
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestTheStatesIconWinsOverTheTiles(t *testing.T) {
	off, _ := mdi.Rune("lightbulb-outline")
	p := describe(Tile{Entity: "light.a", State: "off", Icon: json.RawMessage(itoa(int(off)))}, options{Icon: json.RawMessage(`"F0335"`)})
	if p.Icon != "lightbulb-outline" {
		t.Errorf("icon %q, want the state's", p.Icon)
	}
}

func TestBarItems(t *testing.T) {
	sun, _ := mdi.Rune("weather-sunny")
	items := barItems(json.RawMessage(`[{"k":"clock"},{"k":"wifi","t":"%"},{"k":"text","t":"13 °C","i":` + itoa(int(sun)) + `,"c":"FFB300"},{"k":"ago","e":1760000000},{"k":"analog"}]`))
	if len(items) != 4 || items[0].Kind != "clock" || items[1].Text != "13 °C" || items[1].Icon != "weather-sunny" ||
		items[1].Color != "FFB300" || items[2].At.Unix() != 1760000000 || items[3].Kind != "clock" {
		t.Errorf("bar items %+v", items)
	}
}

// calls listens for the actions the screen calls, until the test ends.
func calls(t *testing.T) func() []component.Call {
	var mu sync.Mutex
	var got []component.Call
	stop := component.CallService.Listen(func(c component.Call) {
		mu.Lock()
		got = append(got, c)
		mu.Unlock()
	})
	t.Cleanup(stop)
	return func() []component.Call {
		mu.Lock()
		defer mu.Unlock()
		return append([]component.Call(nil), got...)
	}
}

func TestATapCallsTheActionAndShowsItUntilHomeAssistantSays(t *testing.T) {
	r, _, seq := committed(t)
	got := calls(t)
	s := newScreen(r, grid{columns: 4, rows: 2})
	now := time.Now()

	s.Tap(0, "light.ceiling", now) // light.ceiling, on
	if c := got(); len(c) != 1 || c[0].Service != "light.toggle" || c[0].Data["entity_id"] != "light.ceiling" {
		t.Fatalf("calls %+v", c)
	}
	if p := s.View(now).Pages[0].Tiles[0]; p.On || p.Value != "Off" {
		t.Errorf("after the tap the light shows on=%v %q, want off at once", p.On, p.Value)
	}
	if p := s.View(now.Add(pendingFor + time.Second)).Pages[0].Tiles[0]; !p.On {
		t.Error("with nothing back from Home Assistant, the light still shows off after the wait")
	}
	// Home Assistant says it is off: that stands, and what was asked is done with.
	r.Receive(packet(seq+1, rev1, `"op":"state","i":0,"entity":"light.ceiling","name":"Ceiling","state":"off","a":{}`))
	if p := s.View(now).Pages[0].Tiles[0]; p.On {
		t.Error("the state Home Assistant sent is not shown")
	}

	s.Tap(1, "sensor.outside", now) // a sensor: nothing to call
	if c := got(); len(c) != 1 {
		t.Errorf("a tap on a sensor called %+v", c[1:])
	}
}

func TestASlideSetsTheLevel(t *testing.T) {
	r, _, _ := committed(t)
	got := calls(t)
	s := newScreen(r, grid{columns: 4, rows: 2})
	now := time.Now()
	s.SetLevel(0, "light.ceiling", 40, now)
	c := got()
	if len(c) != 1 || c[0].Service != "light.turn_on" || c[0].Data["entity_id"] != "light.ceiling" || c[0].Templates["brightness_pct"] != "{{ 40 }}" {
		t.Fatalf("calls %+v", c)
	}
	if p := s.View(now).Pages[0].Tiles[0]; p.Level == nil || p.Level.Value < 39.5 || p.Level.Value > 40.5 || p.Value != "On · 40%" {
		t.Errorf("after the slide the light shows %q", p.Value)
	}
	s.SetLevel(0, "light.ceiling", 0, now)
	if c := got(); c[len(c)-1].Service != "light.turn_off" {
		t.Errorf("a light slid to nothing called %s", c[len(c)-1].Service)
	}
}

func TestTapServices(t *testing.T) {
	for _, c := range []struct{ entity, state, want string }{
		{"switch.a", "on", "switch.toggle"},
		{"cover.a", "closed", "cover.open_cover"},
		{"cover.a", "open", "cover.close_cover"},
		{"cover.a", "opening", "cover.stop_cover"},
		{"lock.a", "unlocked", "lock.lock"},
		{"lock.a", "locked", "lock.unlock"},
		{"scene.a", "x", "scene.turn_on"},
		{"input_button.a", "x", "input_button.press"},
		{"media_player.a", "playing", "media_player.media_play_pause"},
		{"sensor.a", "1", ""},
		{"light.a", "unavailable", ""},
	} {
		if got, _ := tapService(c.entity, c.state); got != c.want {
			t.Errorf("tap on %s %s calls %q, want %q", c.entity, c.state, got, c.want)
		}
	}
}

func TestShownMessagesAreHeard(t *testing.T) {
	r, _, seq := committed(t)
	heard := 0
	r.drawn = func() { heard++ }
	r.Receive(packet(seq+1, rev1, `"op":"ping"`))
	if heard != 0 {
		t.Error("a ping asked for the screen to be drawn again")
	}
	r.Receive(packet(seq+2, rev1, `"op":"state","i":2,"entity":"switch.kettle","name":"Kettle","state":"on","a":{}`))
	if heard != 1 {
		t.Errorf("a state was heard %d times, want once", heard)
	}
}

func TestCurrentIsOffWithoutAScreen(t *testing.T) {
	old := current.Swap(nil)
	t.Cleanup(func() { current.Store(old) })
	if _, on := Current(); on {
		t.Error("Current says on with no screen")
	}
}

// screenOf is a screen on a 4 by 2 grid holding one page of tiles, each given as its entity, state,
// attributes, options and extras.
func screenOf(t *testing.T, tiles ...[5]string) *Screen {
	t.Helper()
	l := &Layout{Rev: rev1, Begin: Begin{Pages: 1, Tiles: len(tiles)}, Pages: []Page{{P: 0, Title: "Home"}}}
	for i, x := range tiles {
		slot := i
		tile := Tile{I: i, Slot: &slot, Entity: x[0], Name: "Tile", State: x[1], A: json.RawMessage(x[2]), O: json.RawMessage(x[3])}
		if x[4] != "" {
			tile.X = json.RawMessage(x[4])
		}
		l.Tiles = append(l.Tiles, tile)
	}
	r := NewReceiver(Capabilities{MaxTiles: 64, MaxPages: 8}, nil)
	r.Restore(l)
	return newScreen(r, grid{columns: 4, rows: 2})
}

// A locked lock asks for a second tap, which unlocks it; the ask runs out, and a first tap after that
// asks again. Locking takes one tap, and a lock the layout lets a tap only lock is not unlocked at all.
func TestALockAsksTwiceBeforeUnlocking(t *testing.T) {
	got := calls(t)
	s := screenOf(t,
		[5]string{"lock.front", "locked", `{}`, `{}`, ""},
		[5]string{"lock.back", "unlocked", `{}`, `{}`, ""},
		[5]string{"lock.shed", "locked", `{}`, `{"guard":"lock_only"}`, ""},
	)
	now := time.Now()

	s.Tap(0, "lock.front", now)
	if c := got(); len(c) != 0 {
		t.Fatalf("the first tap called %+v", c)
	}
	if p := s.View(now).Pages[0].Tiles[0]; !p.Confirm || p.Value != "Tap again to unlock" {
		t.Errorf("after the first tap the lock shows confirm=%v %q", p.Confirm, p.Value)
	}
	if p := s.View(now.Add(confirmFor)).Pages[0].Tiles[0]; p.Confirm {
		t.Error("the ask has not run out")
	}
	later := now.Add(confirmFor + time.Second)
	s.Tap(0, "lock.front", later)
	if c := got(); len(c) != 0 {
		t.Fatalf("a tap after the ask ran out called %+v", c)
	}
	s.Tap(0, "lock.front", later.Add(time.Second))
	if c := got(); len(c) != 1 || c[0].Service != "lock.unlock" || c[0].Data["entity_id"] != "lock.front" {
		t.Fatalf("the second tap called %+v", c)
	}
	if p := s.View(later.Add(time.Second)).Pages[0].Tiles[0]; p.Confirm || p.Value != "Unlocking" {
		t.Errorf("after the second tap the lock shows confirm=%v %q", p.Confirm, p.Value)
	}

	s.Tap(1, "lock.back", now)
	if c := got(); len(c) != 2 || c[1].Service != "lock.lock" {
		t.Errorf("locking called %+v", c[1:])
	}

	if p := s.View(now).Pages[0].Tiles[2]; p.Taps {
		t.Error("a lock the layout lets a tap only lock takes a tap while locked")
	}
	s.Tap(2, "lock.shed", now)
	s.Tap(2, "lock.shed", now)
	if c := got(); len(c) != 2 {
		t.Errorf("a lock the layout lets a tap only lock called %+v", c[2:])
	}
}

// A thermostat's tap opens its card, as one whose layout says so does; one told to do nothing does
// nothing, and a tile told to open a card the screen does not have does nothing either.
func TestTapsThatOpenTheCard(t *testing.T) {
	got := calls(t)
	s := screenOf(t,
		[5]string{"climate.hall", "heat", `{"temperature":21}`, `{}`, ""},
		[5]string{"light.desk", "on", `{}`, `{"tap":"detail"}`, ""},
		[5]string{"light.hall", "on", `{}`, `{"tap":"none"}`, ""},
		[5]string{"switch.fan", "on", `{}`, `{"tap":"detail"}`, ""},
	)
	now := time.Now()
	entities := []string{"climate.hall", "light.desk", "light.hall", "switch.fan"}
	for i, want := range []bool{true, true, false, false} {
		if got := s.Tap(i, entities[i], now); got != want {
			t.Errorf("tile %d: opens its card %v, want %v", i, got, want)
		}
	}
	if c := got(); len(c) != 0 {
		t.Errorf("taps called %+v", c)
	}
	v := s.View(now).Pages[0].Tiles
	if !v[0].Taps || !v[0].Detail || v[2].Taps || v[3].Taps {
		t.Errorf("taps %v %v %v %v", v[0].Taps, v[0].Detail, v[2].Taps, v[3].Taps)
	}
}

// A weather tile has the days of its forecast; a player's artist comes with the extras; a fan with a
// speed has it as a level a finger slides.
func TestForecastArtistAndFanSpeed(t *testing.T) {
	got := calls(t)
	s := screenOf(t,
		[5]string{"weather.home", "sunny", `{"temperature":14}`, `{"size":"wide"}`, `{"days":[{"d":"Mo","c":"rainy","h":15.4,"l":8},{"d":"Tu","c":"sunny","h":17}]}`},
		[5]string{"media_player.den", "playing", `{"media_title":"Song"}`, `{}`, `{"artist":"Band"}`},
		[5]string{"fan.bed", "on", `{"percentage":66,"percentage_step":33.3}`, `{}`, ""},
	)
	now := time.Now()
	v := s.View(now).Pages[0].Tiles
	if d := v[0].Days; len(d) != 2 || d[0].Name != "Mo" || d[0].Icon != "weather-rainy" || d[0].High != 15.4 || !d[0].HasLow || d[1].HasLow {
		t.Errorf("days %+v", d)
	}
	if v[1].Value != "Song · Band" {
		t.Errorf("player %q", v[1].Value)
	}
	if l := v[2].Level; l == nil || l.Kind != "percentage" || l.Value != 66 {
		t.Fatalf("fan level %+v", l)
	}
	s.SetLevel(2, "fan.bed", 33.3, now)
	s.SetLevel(2, "fan.bed", 0, now)
	c := got()
	if len(c) != 2 || c[0].Service != "fan.set_percentage" || c[0].Templates["percentage"] != "{{ 33.3 }}" || c[1].Service != "fan.turn_off" {
		t.Errorf("calls %+v", c)
	}
}

// A sheet reads an entity from its tile, with its name, a player's artist and the color mode a light
// is in put in; what it sets goes as the device's own action, what is not text as a template.
func TestSheetsReadTilesAndCallAsTheDevice(t *testing.T) {
	old := current.Swap(screenOf(t,
		[5]string{"light.white", "on", `{"color_temp_kelvin":3000,"hs_color":[30,40]}`, `{}`, ""},
		[5]string{"light.hue", "on", `{"hs_color":[200,80]}`, `{}`, ""},
		[5]string{"media_player.den", "paused", `{}`, `{}`, `{"artist":"Band"}`},
	))
	t.Cleanup(func() { current.Store(old) })

	if st, a, ok := Entity("light.white"); !ok || st != "on" || a["color_mode"] != "color_temp" || a["friendly_name"] != "Tile" {
		t.Errorf("light.white: %v %v %v", st, a, ok)
	}
	if _, a, _ := Entity("light.hue"); a["color_mode"] != "hs" {
		t.Errorf("light.hue: %v", a)
	}
	if _, a, _ := Entity("media_player.den"); a["media_artist"] != "Band" {
		t.Errorf("player: %v", a)
	}
	if _, _, ok := Entity("light.nowhere"); ok {
		t.Error("an entity on no tile was found")
	}

	got := calls(t)
	Call("media_player.den", "media_player.join", map[string]any{"group_members": []any{"media_player.kitchen"}})
	Call("light.hue", "light.turn_on", map[string]any{"hs_color": []any{120.0, 100.0}, "transition": 0.3})
	Call("x", "music_assistant.play_media", map[string]any{"entity_id": "media_player.ma", "media_id": "uri"})
	c := got()
	if len(c) != 3 {
		t.Fatalf("calls %+v", c)
	}
	if c[0].Data["entity_id"] != "media_player.den" || c[0].Templates["group_members"] != `{{ ["media_player.kitchen"] }}` {
		t.Errorf("join %+v", c[0])
	}
	if c[1].Templates["hs_color"] != "{{ [120,100] }}" || c[1].Templates["transition"] != "{{ 0.3 }}" {
		t.Errorf("color %+v", c[1])
	}
	if c[2].Data["entity_id"] != "media_player.ma" || c[2].Data["media_id"] != "uri" {
		t.Errorf("play %+v", c[2])
	}
}

// A card's controls are the rows the add-on says it draws, of those drawn here; a thermostat's keys
// step it and choose its mode, a cover's open, stop and close it, and a player's volume is a level.
func TestControls(t *testing.T) {
	for _, c := range []struct{ domain, key, want string }{
		{"climate", "setpoint_mode", "setpoint mode"},
		{"media_player", "volume_playback", "volume playback"},
		{"cover", "position_tilt", "position"},
		{"cover", "tilt", ""},
		{"light", "toggle", ""},
		{"light", "brightness", "brightness"},
		{"fan", "speed", "speed"},
		{"sensor", "playback", ""},
	} {
		if got := strings.Join(controlRows(c.domain, c.key), " "); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.domain, c.key, got, c.want)
		}
	}

	got := calls(t)
	s := screenOf(t,
		[5]string{"climate.hall", "heat", `{"temperature":21,"min_temp":7,"max_temp":30,"target_temp_step":0.5,"hvac_modes":["off","heat"]}`, `{"size":"full","controls":"setpoint_mode"}`, ""},
		[5]string{"cover.garage", "closed", `{}`, `{"size":"wide","controls":"buttons"}`, ""},
		[5]string{"media_player.den", "playing", `{"volume_level":0.4}`, `{"size":"wide","controls":"volume"}`, ""},
	)
	now := time.Now()
	v := s.View(now).Pages[0].Tiles
	if strings.Join(v[0].Controls, " ") != "setpoint mode" || len(v[0].Modes) != 2 || v[2].Volume != 0.4 {
		t.Errorf("controls %v modes %v volume %v", v[0].Controls, v[0].Modes, v[2].Volume)
	}
	s.Control(0, "climate.hall", "up", now)
	s.Control(0, "climate.hall", "mode:off", now)
	s.Control(1, "cover.garage", "open", now)
	s.Control(1, "cover.garage", "stop", now)
	s.SetLevel(2, "media_player.den", 55, now)
	c := got()
	if len(c) != 5 {
		t.Fatalf("calls %+v", c)
	}
	if c[0].Service != "climate.set_temperature" || c[0].Templates["temperature"] != "{{ 21.5 }}" {
		t.Errorf("up %+v", c[0])
	}
	if c[1].Service != "climate.set_hvac_mode" || c[1].Data["hvac_mode"] != "off" {
		t.Errorf("mode %+v", c[1])
	}
	if c[2].Service != "cover.open_cover" || c[3].Service != "cover.stop_cover" || c[2].Data["entity_id"] != "cover.garage" {
		t.Errorf("cover %+v %+v", c[2], c[3])
	}
	if c[4].Service != "media_player.volume_set" || c[4].Templates["volume_level"] != "{{ 0.55 }}" {
		t.Errorf("volume %+v", c[4])
	}
}

// A tap goes by what the tile shows: two quick steps of a thermostat step twice, and a second tap on a
// cover the first set opening stops it.
func TestQuickTapsGoByWhatIsShown(t *testing.T) {
	got := calls(t)
	s := screenOf(t,
		[5]string{"climate.hall", "heat", `{"temperature":21,"min_temp":7,"max_temp":30,"target_temp_step":0.5}`, `{"size":"square","controls":"setpoint"}`, ""},
		[5]string{"cover.blind", "closed", `{}`, `{}`, ""},
	)
	now := time.Now()
	s.Control(0, "climate.hall", "up", now)
	s.Control(0, "climate.hall", "up", now.Add(300*time.Millisecond))
	s.Tap(1, "cover.blind", now)
	if p := s.View(now).Pages[0].Tiles[1]; p.State != "opening" {
		t.Errorf("after the first tap the cover shows %q", p.State)
	}
	s.Tap(1, "cover.blind", now.Add(time.Second))
	c := got()
	if len(c) != 4 {
		t.Fatalf("calls %+v", c)
	}
	if c[0].Templates["temperature"] != "{{ 21.5 }}" || c[1].Templates["temperature"] != "{{ 22 }}" {
		t.Errorf("two steps up set %s then %s", c[0].Templates["temperature"], c[1].Templates["temperature"])
	}
	if c[2].Service != "cover.open_cover" || c[3].Service != "cover.stop_cover" {
		t.Errorf("two taps on a closed cover called %s then %s", c[2].Service, c[3].Service)
	}
	if p := s.View(now.Add(time.Second)).Pages[0].Tiles[1]; p.State != "closed" {
		t.Errorf("after the stop the cover shows %q, want what Home Assistant last said", p.State)
	}
}

// A finger acts on the tile it landed on: one that shows another entity by the time it lifts, as after
// a new layout, is left alone.
func TestATileThatChangedIsLeftAlone(t *testing.T) {
	got := calls(t)
	s := screenOf(t,
		[5]string{"switch.kettle", "off", `{}`, `{}`, ""},
		[5]string{"cover.blind", "closed", `{"current_position":0}`, `{}`, ""},
	)
	now := time.Now()
	if s.Tap(0, "light.ceiling", now) {
		t.Error("a tap meant for another entity opened a card")
	}
	s.SetLevel(1, "light.ceiling", 40, now)
	s.Control(1, "light.ceiling", "open", now)
	if c := got(); len(c) != 0 {
		t.Errorf("calls %+v", c)
	}
	if p := s.View(now).Pages[0].Tiles[0]; p.State != "off" {
		t.Errorf("the switch shows %q", p.State)
	}
}

// A closed garage door, gate or door asks for a second tap, on the tile or its open key, before it
// opens; shutting one takes one tap, and a blind is opened on the first.
func TestAGarageDoorAsksTwiceBeforeOpening(t *testing.T) {
	got := calls(t)
	s := screenOf(t,
		[5]string{"cover.garage", "closed", `{"device_class":"garage"}`, `{}`, ""},
		[5]string{"cover.drive", "closed", `{"device_class":"gate"}`, `{"size":"wide","controls":"buttons"}`, ""},
		[5]string{"cover.side", "open", `{"device_class":"door"}`, `{}`, ""},
		[5]string{"cover.blind", "closed", `{"device_class":"blind"}`, `{}`, ""},
	)
	now := time.Now()
	s.Tap(0, "cover.garage", now)
	if c := got(); len(c) != 0 {
		t.Fatalf("the first tap called %+v", c)
	}
	if p := s.View(now).Pages[0].Tiles[0]; !p.Confirm || p.Value != "Tap again to open" {
		t.Errorf("after the first tap the garage shows confirm=%v %q", p.Confirm, p.Value)
	}
	s.Tap(0, "cover.garage", now.Add(time.Second))
	if c := got(); len(c) != 1 || c[0].Service != "cover.open_cover" || c[0].Data["entity_id"] != "cover.garage" {
		t.Fatalf("the second tap called %+v", c)
	}

	s.Control(1, "cover.drive", "open", now)
	if c := got(); len(c) != 1 {
		t.Fatalf("the gate's open key called %+v at once", c[1:])
	}
	s.Control(1, "cover.drive", "open", now.Add(time.Second))
	if c := got(); len(c) != 2 || c[1].Service != "cover.open_cover" || c[1].Data["entity_id"] != "cover.drive" {
		t.Fatalf("the gate's second open called %+v", c[1:])
	}

	s.Tap(2, "cover.side", now)
	s.Tap(3, "cover.blind", now)
	if c := got(); len(c) != 4 || c[2].Service != "cover.close_cover" || c[3].Service != "cover.open_cover" {
		t.Errorf("shutting a door and opening a blind called %+v", c[2:])
	}
}

// A tap on a player plays or pauses it where its keys do, off the finger's goroutine, and shows it at once.
func TestAPlayersTapGoesWhereItsKeysDo(t *testing.T) {
	was := Transport
	Transport = func(entity string) string { return "media_player.ma_den" }
	t.Cleanup(func() { Transport = was })
	got := calls(t)
	s := screenOf(t, [5]string{"media_player.den", "playing", `{}`, `{}`, ""})
	now := time.Now()
	s.Tap(0, "media_player.den", now)
	if p := s.View(now).Pages[0].Tiles[0]; p.State != "paused" {
		t.Errorf("after the tap the player shows %q", p.State)
	}
	deadline := time.Now().Add(time.Second)
	for len(got()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c := got(); len(c) != 1 || c[0].Service != "media_player.media_play_pause" || c[0].Data["entity_id"] != "media_player.ma_den" {
		t.Errorf("calls %+v", c)
	}
}

// A state's first letter is a whole letter, however many bytes it takes.
func TestHumanTakesAWholeFirstLetter(t *testing.T) {
	for in, want := range map[string]string{"not_home": "Not home", "été": "Été", "über_cool": "Über cool", "": ""} {
		if got := human(in); got != want {
			t.Errorf("human(%q) = %q, want %q", in, got, want)
		}
	}
}

// A thermostat with no range between its lowest and highest has no level to slide along.
func TestAThermostatWithNoRangeHasNoLevel(t *testing.T) {
	if l := level("climate", "heat", map[string]any{"temperature": 21.0, "min_temp": 30.0, "max_temp": 30.0}); l != nil {
		t.Errorf("level %+v", l)
	}
	if l := level("climate", "heat", map[string]any{"temperature": 21.0}); l == nil {
		t.Error("a thermostat with the usual range has no level")
	}
}
