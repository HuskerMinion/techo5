//go:build mockup && !dot && !spot

package display

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/tessera"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// Mockups of the Tessera page on the Show 5, the Show 8 and the Skylight, each with the grid it tells
// the add-on it has (MOCK_OUT): a sample layout's pages, a light's level being slid, a lock asking to
// be sure, and the sheets a long press opens.
func TestMockTessera(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Mode(config.DashboardTessera); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tessera.Preview(nil, 0, 0) })
	previewFont(t)
	out := os.Getenv("MOCK_OUT")
	at := time.Date(2026, 10, 19, 18, 42, 0, 0, time.Local)
	for _, panel := range []struct {
		name          string
		wide, high    int
		columns, rows int
	}{{"show5", showWide, showHigh, 4, 2}, {"show8", show8Wide, show8High, 4, 3}, {"skylight", skylightWide, skylightHigh, 5, 3}} {
		v := tessera.Place(mockLayout(t, panel.columns, panel.rows), panel.columns, panel.rows)
		var tiles []mockTile
		for p := range v.Pages {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			s := scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true, tessPage: p}
			newRenderer(img).draw(s)
			name := fmt.Sprintf("tessera-%s-page%d", panel.name, p+1)
			mockSave(t, out, name, img)
			tiles = append(tiles, mockTile{name, img})
		}
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		s := scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true,
			dashAdjust: dashAdjusting{entity: "light.kitchen", value: 35}}
		newRenderer(img).draw(s)
		mockSave(t, out, "tessera-"+panel.name+"-sliding", img)
		tiles = append(tiles, mockTile{"sliding", img})
		img = image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		newRenderer(img).draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessOn: true})
		mockSave(t, out, "tessera-"+panel.name+"-waiting", img)
		tiles = append(tiles, mockTile{"waiting", img})

		// The lock asking to be sure, after a first tap.
		l := mockLayout(t, panel.columns, panel.rows)
		tessera.Preview(l, panel.columns, panel.rows)
		for i, tile := range l.Tiles {
			if tile.Entity == "lock.front" {
				tessera.Tap(i, tile.Entity)
			}
		}
		v, _ = tessera.Current()
		img = image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		newRenderer(img).draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true, tessPage: 3})
		mockSave(t, out, "tessera-"+panel.name+"-confirm", img)
		tiles = append(tiles, mockTile{"confirm", img})

		// The sheets, read from the tiles as the page's long press reads them.
		src := tessSheets{}
		color, ok := src.LightColor("light.ceiling")
		if !ok {
			t.Fatal("the ceiling light has no colors")
		}
		climate, ok := src.ClimateNow("climate.living_room")
		if !ok {
			t.Fatal("the thermostat is not read")
		}
		media, ok := src.MediaNow("media_player.kitchen")
		if !ok {
			t.Fatal("the player is not read")
		}
		for _, sh := range []struct {
			name string
			s    scene
		}{
			{"color", scene{tessPage: 1, dashColor: &color}},
			{"climate", scene{dashClimate: &climateView{now: climate}}},
			{"media", scene{dashMedia: &mediaView{now: media, sheet: mediaSheet{entity: media.Entity, name: media.Name, volume: -1,
				grouped: map[string]bool{"media_player.office": true},
				lists: dashboard.MediaLists{MusicAssistant: true, Target: media.Entity, Group: media.Entity, Queued: true,
					Favorites: []dashboard.MediaChoice{{Name: "Morning jazz", Kind: "playlist"}, {Name: "Radio Paradise", Kind: "radio"}, {Name: "Kind of Blue", Kind: "album"}, {Name: "Focus", Kind: "playlist"}},
					Speakers:  []dashboard.Speaker{{Entity: "media_player.bedroom", Name: "Bedroom", Volume: 0.3}, {Entity: "media_player.office", Name: "Office", Volume: 0.5}}}}}}},
		} {
			s := sh.s
			s.now, s.phase, s.showDash, s.dashMode, s.tessera, s.tessOn = at, "idle", true, config.DashboardTessera, v, true
			img = image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			newRenderer(img).draw(s)
			mockSave(t, out, "tessera-"+panel.name+"-sheet-"+sh.name, img)
			tiles = append(tiles, mockTile{"sheet " + sh.name, img})
		}
		mockSheet(t, out, "tessera-"+panel.name+"-sheet", tiles, 2)
	}
}

// mockControls are the controls chosen for some of the sample layout's tiles in Tessera, by page and
// entity: the thermostat's page has its setpoint and modes, the tall fan its speed, the wide player its
// keys.
var mockControls = map[string]string{
	"2/climate.living_room": "setpoint_mode",
	"3/fan.bedroom":         "speed",
	"3/media_player.living": "playback",
}

// mockExtras are the extras of the sample layout's tiles, by entity: a forecast, a player's artist.
var mockExtras = map[string]string{
	"weather.home": `{"days":[{"d":"Mon","c":"rainy","h":14.2,"l":8},{"d":"Tue","c":"partlycloudy","h":16,"l":9},` +
		`{"d":"Wed","c":"sunny","h":19,"l":10},{"d":"Thu","c":"cloudy","h":15,"l":11},{"d":"Fri","c":"pouring","h":12,"l":7}]}`,
	"media_player.living": `{"artist":"Miles Davis","album":"Kind of Blue"}`,
}

// mockTessTile is a tile of the sample layout, at a page, row and column of a grid.
type mockTessTile struct {
	page, row, col int
	size           string
	entity, name   string
	state          string
	attrs          map[string]any
	icon           string // an mdi name the layout gives it, or none
	background     string
}

// mockLayout is a sample layout for a grid of columns by rows: a home page that fills it, a page of
// lights and a detail page. Tiles that would fall off a smaller grid are left out, as the add-on's
// editor would not have put them there.
func mockLayout(t *testing.T, columns, rows int) *tessera.Layout {
	t.Helper()
	tiles := []mockTessTile{
		{0, 0, 0, "", "light.kitchen", "Kitchen", "on", map[string]any{"brightness": 180, "supported_color_modes": []string{"brightness"}}, "", ""},
		{0, 0, 1, "", "switch.kettle", "Kettle", "off", nil, "kettle", ""},
		{0, 0, 2, "wide", "sensor.outside", "Outside", "12.5", map[string]any{"unit_of_measurement": "°C", "device_class": "temperature"}, "", "blue"},
		{0, 1, 0, "", "climate.living_room", "Living room", "heat", map[string]any{"temperature": 21, "current_temperature": 20.5, "min_temp": 7, "max_temp": 30, "hvac_modes": []string{"off", "heat", "cool", "auto"}}, "", ""},
		{0, 1, 1, "", "media_player.kitchen", "Kitchen speaker", "playing", map[string]any{"media_title": "Clair de lune", "media_artist": "Debussy"}, "", ""},
		{0, 1, 2, "", "binary_sensor.front_door", "Front door", "off", map[string]any{"device_class": "door"}, "", ""},
		{0, 1, 3, "", "fan.bedroom", "Bedroom fan", "off", nil, "", ""},
		{0, 2, 0, "wide", "weather.home", "Home", "partlycloudy", map[string]any{"temperature": 13, "temperature_unit": "°C"}, "", ""},
		{0, 2, 2, "", "sensor.humidity", "Humidity", "48", map[string]any{"unit_of_measurement": "%", "device_class": "humidity"}, "", "mint"},
		{0, 2, 3, "", "input_boolean.guest_mode", "Guest mode", "on", nil, "", ""},
		{0, 0, 4, "", "person.you", "You", "home", nil, "", ""},
		{0, 1, 4, "", "sensor.power", "Power", "1240", map[string]any{"unit_of_measurement": "W", "device_class": "power"}, "", "yellow"},
		{0, 2, 4, "", "lock.back_door", "Back door", "unlocked", nil, "", ""},

		{1, 0, 0, "", "light.ceiling", "Ceiling", "on", map[string]any{"brightness": 255, "supported_color_modes": []string{"color_temp", "hs"}, "color_temp_kelvin": 3000, "min_color_temp_kelvin": 2200, "max_color_temp_kelvin": 6500}, "ceiling-light", ""},
		{1, 0, 1, "", "light.lamp", "Reading lamp in the living room", "off", map[string]any{"supported_color_modes": []string{"brightness"}}, "lamp", ""},
		{1, 0, 2, "", "cover.garage", "Garage", "closed", map[string]any{"current_position": 0}, "garage", ""},
		{1, 0, 3, "", "scene.movie", "Movie night", "2026-10-19T17:00:00+00:00", nil, "television", "purple"},
		{1, 1, 0, "", "light.porch", "Porch", "unavailable", nil, "", ""},
		{1, 1, 1, "", "script.goodnight", "Good night", "off", nil, "bed", ""},
		{1, 1, 2, "", "cover.blinds", "Blinds", "open", map[string]any{"current_position": 60}, "", ""},
		{1, 1, 3, "", "button.doorbell_test", "Doorbell", "unknown", nil, "", ""},

		{2, 0, 0, "full", "climate.living_room", "Living room", "heat", map[string]any{"temperature": 21, "current_temperature": 20.5, "min_temp": 7, "max_temp": 30, "target_temp_step": 0.5, "hvac_modes": []string{"off", "heat", "cool", "auto"}}, "", ""},

		// The sizes past one row, and the kinds that use them.
		{3, 0, 0, "square", "weather.home", "Forecast", "partlycloudy", map[string]any{"temperature": 13, "temperature_unit": "°C"}, "", ""},
		{3, 0, 2, "tall", "fan.bedroom", "Bedroom fan", "on", map[string]any{"percentage": 66, "percentage_step": 33.3}, "", "mint"},
		{3, 0, 3, "", "lock.front", "Front door", "locked", nil, "", ""},
		{3, 1, 3, "", "cover.garage", "Garage", "open", map[string]any{"current_position": 100}, "garage", ""},
		{3, 2, 0, "3x1", "media_player.living", "Living room", "playing", map[string]any{"media_title": "So What", "volume_level": 0.4}, "", ""},
		{3, 2, 3, "", "scene.relax", "Relax", "2026-10-19T17:00:00+00:00", nil, "sofa", "purple"},
		{3, 0, 4, "1x3", "weather.home", "Week", "partlycloudy", map[string]any{"temperature": 13, "temperature_unit": "°C"}, "", "blue"},
	}
	cells := columns * rows
	l := &tessera.Layout{
		Rev:   "0123456789abcdef",
		Begin: tessera.Begin{Title: "Kitchen", Pages: 4, Home: 0},
		Pages: []tessera.Page{
			{P: 0, ID: "home", Title: "Home", Items: mockBar(t)},
			{P: 1, ID: "lights", Title: "Lights", Items: json.RawMessage(`[{"k":"date"}]`)},
			{P: 2, ID: "climate", Title: "Climate", Excluded: true, Items: json.RawMessage(`[]`)},
			{P: 3, ID: "more", Title: "Around the house", Items: json.RawMessage(`[]`)},
		},
	}
	for _, m := range tiles {
		if m.row >= rows || m.col >= columns {
			continue
		}
		slot := m.page*cells + m.row*columns + m.col
		o := map[string]any{}
		if m.size != "" {
			o["size"] = m.size
		}
		if m.icon != "" {
			r, ok := mdi.Rune(m.icon)
			if !ok {
				t.Fatalf("no icon %s", m.icon)
			}
			o["icon"] = int(r)
		}
		if m.background != "" {
			o["background"] = m.background
		}
		if c, ok := mockControls[fmt.Sprintf("%d/%s", m.page, m.entity)]; ok {
			o["controls"] = c
		}
		a, _ := json.Marshal(m.attrs)
		ob, _ := json.Marshal(o)
		tile := tessera.Tile{I: len(l.Tiles), Slot: &slot, Entity: m.entity, Name: m.name, State: m.state, A: a, O: ob}
		if x, ok := mockExtras[m.entity]; ok {
			tile.X = json.RawMessage(x)
		}
		l.Tiles = append(l.Tiles, tile)
	}
	l.Begin.Tiles = len(l.Tiles)
	return l
}

// mockBar is a top bar with the outside temperature and an entity's words.
func mockBar(t *testing.T) json.RawMessage {
	t.Helper()
	sun, _ := mdi.Rune("weather-partly-cloudy")
	washer, _ := mdi.Rune("washing-machine")
	b, _ := json.Marshal([]map[string]any{
		{"k": "text", "t": "Washer done", "i": int(washer), "c": "6AB86E"},
		{"k": "text", "t": "13 °C", "i": int(sun)},
	})
	return b
}
