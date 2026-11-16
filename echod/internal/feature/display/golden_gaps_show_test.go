//go:build !dot && !spot

package display

import (
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/tessera"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The screens the other previews left out, drawn so the golden check (tools/golden) can hold them: a
// voice turn on the Show, the Tessera page, the portrait panel in a light theme and in a skin, the
// wake light with its Skip button, and a few pages on a 2560 by 1600 tablet. Each draws whether or not
// SHOW_PREVIEW is set; with it set, each is written there.

// panelBig is a large Android tablet's screen, the biggest a TECHO5 Panel is likely to be drawn on.
// No Echo has it; the renderer draws at any size, so it stands in as the stress size for the scaling.
const (
	panelBigWide = 2560
	panelBigHigh = 1600
)

// The portrait panel a tablet stood on end draws, as the Commodore 64 previews name it.
const (
	portraitWide = 800
	portraitHigh = 1280
)

func goldenSave(t *testing.T, name string, img *image.RGBA) {
	t.Helper()
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func goldenDraw(t *testing.T, name string, wide, high int, s scene) *renderer {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, wide, high))
	r := newRenderer(img)
	r.draw(s)
	goldenSave(t, name, img)
	return r
}

// A turn on the Show 8, as the Spot's previews have it: the plain pages (listening, the words heard,
// the answer), and the Bars and Orb pictures for the styles that draw one.
func TestShowTurnPreviews(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	since := at.Add(-400 * time.Millisecond)
	heard := "What's the weather tomorrow?"
	reply := "Tomorrow will be sunny, with a high of 74 and a low of 51."
	listen, listenPk := eqVoice(0.62, 1)
	said, saidPk := eqVoice(0.95, 7)
	think := eqFor("thinking", false, at)
	think.style, think.phase = "orb", "thinking"
	for name, s := range map[string]scene{
		"turn-classic-listening": {now: at, since: since, phase: "listening"},
		"turn-classic-thinking":  {now: at, since: since, phase: "thinking", heard: heard},
		"turn-classic-replying":  {now: at, since: since, phase: "replying", heard: heard, reply: reply},
		"turn-bars-listening":    {now: at, since: since, phase: "listening", eq: &eqView{level: listen, peak: listenPk}},
		"turn-bars-replying":     {now: at, since: since, phase: "replying", heard: heard, reply: reply, eq: &eqView{level: said, peak: saidPk}},
		"turn-orb-thinking":      {now: at, since: since, phase: "thinking", heard: heard, eq: think},
	} {
		goldenDraw(t, name+"-show8", show8Wide, show8High, s)
	}
}

// goldenTessLayout is a Tessera page of a handful of tiles: lights on and off, a reading, a switch and
// a lock, enough to see the grid filled on a Show 8.
func goldenTessLayout() *tessera.Layout {
	slot := func(n int) *int { return &n }
	tile := func(i, at int, entity, name, state, a, o string) tessera.Tile {
		return tessera.Tile{I: i, Slot: slot(at), Entity: entity, Name: name, State: state, A: json.RawMessage(a), O: json.RawMessage(o)}
	}
	return &tessera.Layout{
		Rev:   "0123456789abcdef",
		Begin: tessera.Begin{Title: "Kitchen", Pages: 2, Home: 0},
		Pages: []tessera.Page{
			{P: 0, ID: "home", Title: "Home", Items: json.RawMessage(`[{"k":"date"}]`)},
			{P: 1, ID: "more", Title: "More", Items: json.RawMessage(`[]`)},
		},
		Tiles: []tessera.Tile{
			tile(0, 0, "light.kitchen", "Kitchen", "on", `{"brightness":180,"supported_color_modes":["brightness"]}`, `{}`),
			tile(1, 1, "switch.kettle", "Kettle", "off", `{}`, `{}`),
			tile(2, 2, "sensor.outside", "Outside", "12.5", `{"unit_of_measurement":"°C","device_class":"temperature"}`, `{"size":"wide","background":"blue"}`),
			tile(3, 4, "light.porch", "Porch", "off", `{"supported_color_modes":["brightness"]}`, `{}`),
			tile(4, 5, "lock.back_door", "Back door", "locked", `{}`, `{}`),
			tile(5, 6, "sensor.humidity", "Humidity", "48", `{"unit_of_measurement":"%","device_class":"humidity"}`, `{"background":"mint"}`),
			tile(6, 7, "input_boolean.guest_mode", "Guest mode", "on", `{}`, `{}`),
			tile(7, 8, "cover.garage", "Garage", "closed", `{"current_position":0}`, `{}`),
		},
	}
}

// The Tessera dashboard page on the Show 8, its first page, with the grid it tells the add-on it has.
func TestTesseraPreview(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 10, 19, 18, 42, 0, 0, time.Local)
	v := tessera.Place(goldenTessLayout(), 4, 3)
	goldenDraw(t, "tessera-show8", show8Wide, show8High,
		scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true})
}

// The portrait panel's clock and month in a light theme (Paper) and in a dark skin (Earthrise), beside
// the Commodore 64 skins' portrait previews, which were the only ones.
func TestPortraitPreviews(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	keepPalette(t)
	old := skinPreview
	t.Cleanup(func() { skinPreview = old; applyTheme(current()) })
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	clock := scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}}
	month := calendarScenes()["calendar-month"]
	for _, look := range []struct {
		name, theme, skin string
	}{{"Paper", "Paper", skinNone}, {"earthrise", "", skinEarthrise}} {
		skinPreview = look.skin
		applyTheme(themes[themeIndex(look.theme)])
		goldenDraw(t, "clock-"+look.name+"-panel-portrait", portraitWide, portraitHigh, clock)
		goldenDraw(t, "calendar-month-"+look.name+"-panel-portrait", portraitWide, portraitHigh, month)
	}
}

// The wake light half way up with the Skip button for the alarm it is the light of, on the Show 8.
func TestSunriseSkipPreview(t *testing.T) {
	skipState(t)
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	clock24.Store(false)
	now, ring := skipAt(t)
	skipLight(t)
	skipPut(t, config.Alarm{ID: "wake", Hour: 7, Minute: 0, Days: config.DaysEvery, Label: "Wake up", On: true})
	token := alarm.SkipToken{ID: "wake", At: ring}
	skipStubs(t, 0.5, &token, nil)
	r := goldenDraw(t, "sunrise-skip-show8", show8Wide, show8High,
		scene{now: now, phase: "idle", sunrise: 0.5, sunriseFace: true})
	if r.skipDrawn.Empty() {
		t.Fatal("the light's page drew no Skip button")
	}
}

// The clock, the month, the weather and the drawn dashboard on a 2560 by 1600 tablet: the Show 5's
// layout at more than two and a half times its size.
func TestPanelBigPreviews(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	sky := home.Weather{Condition: "partlycloudy", Temp: "72°"}
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "rainy", "lightning-rainy", "sunny", "snowy", "cloudy"} {
		week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: c, High: float64(78 - 3*i), Low: float64(55 - 2*i), Rain: 10 * i})
	}
	for name, s := range map[string]scene{
		"clock":          {now: at, phase: "idle", weather: sky},
		"calendar-month": calendarScenes()["calendar-month"],
		"weather":        {now: at, phase: "idle", weather: sky, forecast: week, showWeather: true},
		"dash-tiles-normal": {now: time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local), phase: "idle", showDash: true,
			dashMode: config.DashboardDrawn, drawn: fourControls()},
	} {
		goldenDraw(t, name+"-2560x1600", panelBigWide, panelBigHigh, s)
	}
}
