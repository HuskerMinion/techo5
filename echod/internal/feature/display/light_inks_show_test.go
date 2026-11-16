//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"os"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// On Paper and Linen the weather's colors, the month's start times and the words on an accent pill
// can all be read: the weather's drawings 3:1 on the ground, as a drawing needs; the rain, which is
// words too, and the 18-pixel start times 4.5:1, the times on a day's box and on today's; and a pill's
// words 4.5:1 on the accent. A dark theme keeps the pale colors it always had.
func TestLightThemesReadTheirInks(t *testing.T) {
	saved := [roles]color.RGBA{walnut, amber, cream, dim, ember}
	t.Cleanup(func() { walnut, amber, cream, dim, ember = saved[0], saved[1], saved[2], saved[3], saved[4] })

	applyTheme(themes[themeIndex("Walnut")])
	if sunInk() != sunGold || cloudInk() != cloudGray || rainInk() != rainBlue || snowInk() != snowWhite {
		t.Error("a dark theme lost the weather's pale colors")
	}
	if c := calendarColors[0]; timeInk(c) != c {
		t.Error("a dark theme's start times are not in the calendar's color")
	}

	for _, name := range []string{"Paper", "Linen"} {
		applyTheme(themes[themeIndex(name)])
		if dark() {
			t.Fatalf("%s is not a light theme", name)
		}
		for _, ink := range []struct {
			name  string
			c     color.RGBA
			least float64
		}{
			{"sun", sunInk(), 3}, {"sun's rays", sunRayInk(), 3}, {"cloud", cloudInk(), 3},
			{"storm cloud", cloudDarkInk(), 3}, {"snow", snowInk(), 3}, {"bolt", boltInk(), 3},
			{"moon", moonInk(), 3}, {"rain", rainInk(), 4.5},
		} {
			got := colorContrast(ink.c, walnut)
			t.Logf("%s: the %s is %.2f:1 on the ground", name, ink.name, got)
			if got < ink.least {
				t.Errorf("%s: the %s is %.2f:1 on the ground, want %.1f", name, ink.name, got, ink.least)
			}
		}
		for _, cond := range []string{"sunny", "clear-night", "partlycloudy", "rainy", "pouring", "lightning",
			"lightning-rainy", "snowy", "snowy-rainy", "hail", "fog", "windy", "exceptional", "cloudy"} {
			if _, c := weatherGlyph(cond); colorContrast(c, walnut) < 3 {
				t.Errorf("%s: a day's %s icon is %.2f:1 on the ground, want 3", name, cond, colorContrast(c, walnut))
			}
		}
		for i, c := range calendarColors {
			box, today := colorContrast(timeInk(c), calWash(10)), colorContrast(timeInk(c), lerp(walnut, amber, 0.12))
			t.Logf("%s: calendar %d's start time is %.2f:1 on a day's box, %.2f:1 on today's", name, i, box, today)
			if min(box, today) < 4.5 {
				t.Errorf("%s: calendar %d's start time is %.2f:1 on a day's box, %.2f:1 on today's, want 4.5", name, i, box, today)
			}
		}
		lightWeatherPreview(t, name)
		pill := colorContrast(onAccent(), amber)
		t.Logf("%s: a pill's words are %.2f:1 on the accent", name, pill)
		if pill < 4.5 {
			t.Errorf("%s: a pill's words are %.2f:1 on the accent, want 4.5", name, pill)
		}
	}

	// A custom light theme with an accent as pale as its ground: the ground color on it was no words at all.
	applyTheme(preset("Custom", 0xfafafa, 0xeaeaea, 0xf6f6f6, 0x888888, 0xcccccc))
	if was, now := colorContrast(walnut, amber), colorContrast(onAccent(), amber); now < 4.5 {
		t.Errorf("on a pale accent a pill's words are %.2f:1 (the ground color was %.2f:1), want 4.5", now, was)
	}
}

// lightWeatherPreview writes the weather page in the theme in force to SHOW_PREVIEW, when it is set:
// every kind of drawing the weather has, on each panel, to look at.
func lightWeatherPreview(t *testing.T, theme string) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		return
	}
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}, {"skylight", skylightWide, skylightHigh}} {
		for _, conds := range [][]string{
			{"lightning-rainy", "sunny", "partlycloudy", "rainy", "snowy", "fog"},
			{"clear-night", home.PartlyCloudyNight, "pouring", "hail", "windy", "cloudy"},
		} {
			var week []hass.Day
			for i, c := range conds {
				week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: c, High: float64(78 - 3*i), Low: float64(55 - 2*i), Rain: 10 * (i + 1)})
			}
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			newRenderer(img).draw(scene{now: at, phase: "idle", weather: home.Weather{Condition: conds[0], Temp: "68°"}, forecast: week, showWeather: true})
			writePNG(t, dir, "weather-"+conds[0]+"-"+theme+"-"+panel.name+".png", img)
		}
	}
}
