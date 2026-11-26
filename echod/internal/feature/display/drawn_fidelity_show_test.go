//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Drawn dashboard cards drawn closer to Home Assistant's own: a markdown card with its structure
// (#757), a weather forecast card (#758), and tiles with Home Assistant's own icons and words (#760). Each draws whether or not SHOW_PREVIEW is set, and with it set is written there for the
// golden check (tools/golden).

var fidelityAt = time.Date(2026, 10, 4, 14, 7, 0, 0, time.Local)

func drawnScene(d dashboard.Drawn) scene {
	return scene{now: fidelityAt, phase: "idle", showDash: true, dashMode: config.DashboardDrawn, drawn: d}
}

// markdownView is #757's card beside two tiles, in a two-section view.
func markdownView() dashboard.Drawn {
	text := "## Today\n**Partly cloudy.** Light winds, turning **gusty** after dark.\n\n- High 24°\n- Rain 20%, mostly in the evening hours after the commute\n\nsnake_case and 2 * 3 stay as written."
	return dashboard.Drawn{Sections: []dashboard.Section{
		{Blocks: []dashboard.Block{{Heading: "Weather"}, {Text: []string{"x"}, Marked: dashboardMarked(text)}}},
		{Blocks: []dashboard.Block{{Heading: "Hallway"}, {Tiles: []dashboard.Tile{
			{Name: "Ceiling", Icon: "lightbulb", Value: "On · 80%", On: true},
			{Name: "Porch", Icon: "outdoor-lamp", Value: "Off"},
		}}}},
	}}
}

// dashboardMarked is the dashboard's own reading of a markdown card's content.
func dashboardMarked(md string) []dashboard.TextLine { return dashboard.MarkText(md) }

func TestDrawnMarkdownPreview(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := goldenDraw(t, "dash-markdown-show8", show8Wide, show8High, drawnScene(markdownView()))
	// The body is drawn smaller than a tile's name, and a heading larger than the body.
	mf := r.markedFaces()
	if mf.body.Metrics().Height >= r.faces().label.Metrics().Height {
		t.Fatal("the markdown body is not smaller than a tile's name")
	}
	if mf.heading(2).Metrics().Height <= mf.body.Metrics().Height {
		t.Fatal("a heading is not larger than the body")
	}
}

// A bullet's words wrap under its words, not under its dot, and a gap is half a line.
func TestDrawnMarkdownLayout(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	lines := dashboard.MarkText("one\n\ntwo")
	_, h := r.layoutMarked(lines, 400)
	step, _ := r.lineStep(r.markedFaces().body)
	if h != 2*step+step/2 {
		t.Fatalf("two lines and a gap came to %d, want %d", h, 2*step+step/2)
	}
	wrapped := r.wrapRuns(r.markedFaces(), []dashboard.TextRun{{Text: "a few "}, {Text: "bold", Bold: true}, {Text: " words that go on and on past the edge"}}, 200)
	if len(wrapped) < 2 {
		t.Fatalf("mixed runs did not wrap: %+v", wrapped)
	}
	for _, line := range wrapped {
		w := 0
		for _, wd := range line {
			f := r.markedFaces().body
			if wd.bold {
				f = r.markedFaces().bold
			}
			w += r.width(f, wd.s)
		}
		if w > 200 {
			t.Fatalf("a wrapped line is %d wide, over 200: %+v", w, line)
		}
	}
}

// iconsView is #760's table as the tiles draw it with Home Assistant's icons and words, and an icon
// named hass: as Home Assistant still writes some.
func iconsView() dashboard.Drawn {
	return dashboard.Drawn{Sections: []dashboard.Section{{Blocks: []dashboard.Block{
		{Heading: "Utility"},
		{Tiles: []dashboard.Tile{
			{Name: "Robot", Icon: "mdi:robot-vacuum", Value: "Docked"},
			{Name: "Phone", Icon: "mdi:battery", Value: "Not charging"},
			{Name: "Dehumidifier tank", Icon: "mdi:water-outline", Value: "OK"},
			{Name: "Mains", Icon: "mdi:sine-wave", Value: "231 V"},
			{Name: "Download", Icon: "mdi:speedometer", Value: "512 Mbit/s"},
			{Name: "Energy meter", Icon: "mdi:counter", Value: "1204 kWh"},
			{Name: "UV risk", Icon: "mdi:eye", Value: "No risk"},
			{Name: "Old icon name", Icon: "hass:flash", Value: "On", On: true},
		}},
	}}}}
}

func TestDrawnIconsPreview(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	goldenDraw(t, "dash-icons-show8", show8Wide, show8High, drawnScene(iconsView()))
}

// forecastView is #758's card, daily, beside one by the hour with no weather now, and a tile.
func forecastView() dashboard.Drawn {
	var daily, hourly []hass.Day
	for i, c := range []string{"partlycloudy", "rainy", "lightning-rainy", "sunny", "snowy", "cloudy", "fog"} {
		daily = append(daily, hass.Day{When: fidelityAt.AddDate(0, 0, i), Condition: c, High: float64(24 - i), Low: float64(13 - i),
			HasHigh: true, HasLow: true})
	}
	for i, c := range []string{"partlycloudy", "cloudy", "rainy", "pouring", "rainy", "cloudy"} {
		hourly = append(hourly, hass.Day{When: fidelityAt.Truncate(time.Hour).Add(time.Duration(i+1) * time.Hour), Condition: c,
			High: float64(22 - i), HasHigh: true})
	}
	return dashboard.Drawn{Sections: []dashboard.Section{
		{Blocks: []dashboard.Block{{Heading: "Weather"},
			{Forecast: &dashboard.Forecast{Current: true, Name: "Forecast Home", Icon: "weather-partly-cloudy", Condition: "Partly cloudy",
				Temp: "22°", Days: daily, Max: 5}}}},
		{Blocks: []dashboard.Block{{Heading: "Today"},
			{Forecast: &dashboard.Forecast{Name: "Forecast Home", Days: hourly, Hourly: true, Max: 5}},
			{Tiles: []dashboard.Tile{{Name: "Porch", Icon: "outdoor-lamp", Value: "Off"}}}}},
	}}
}

func TestDrawnForecastPreview(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := goldenDraw(t, "dash-forecast-show8", show8Wide, show8High, drawnScene(forecastView()))
	// A narrow card, the Spot's column, fits fewer days than the five a wide one shows.
	if wide, narrow := r.daysAcross(560, 1), r.daysAcross(220, 1); wide < 5 || narrow >= 5 || narrow < 2 {
		t.Errorf("days across: %d wide, %d narrow", wide, narrow)
	}
}

// The days are named by the day in the screen's language, an hour's forecast by the hour, and no more
// are kept than the card asks for.
func TestForecastCellsAreNamed(t *testing.T) {
	f := forecastView().Sections[0].Blocks[1].Forecast
	cells := forecastCells(*f, "de")
	if len(cells) != 5 || cells[0].Name != "So" || cells[1].Icon != "weather-rainy" || !cells[0].HasLow {
		t.Errorf("daily, in German: %+v", cells)
	}
	clock24.Store(false)
	h := forecastView().Sections[1].Blocks[1].Forecast
	cells = forecastCells(*h, "en")
	if len(cells) != 5 || cells[0].Name != "3 PM" || cells[0].HasLow {
		t.Errorf("hourly: %+v", cells)
	}
}
