//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Every clock style draws on both panels, alone, with a timer running, with the glance strip, with
// the music strip and with the chips over it; with SHOW_PREVIEW set, each is written there to look at.
func TestShowClockStylesDraw(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	t.Cleanup(func() {
		_ = config.Set().Screen().ClockStyle("")
		_ = config.Set().Screen().GlanceWithMusic("")
	})
	// Chips while music plays: Above the strip, for the scene with both; the others have one or neither.
	if err := config.Set().Screen().GlanceWithMusic("above"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	sky := home.Weather{Condition: "partlycloudy", Temp: "72°"}
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "rainy", "sunny", "cloudy", "snowy"} {
		week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: c, High: float64(72 - 3*i), Low: float64(51 - 2*i)})
	}
	facts := styleFacts{
		rise: time.Date(2026, 9, 16, 6, 48, 0, 0, time.Local), set: time.Date(2026, 9, 16, 19, 5, 0, 0, time.Local), sunOK: true,
		days: week,
		next: []hass.Event{
			{Summary: "Dentist", Calendar: "calendar.family", Start: at.Add(83 * time.Minute), End: at.Add(143 * time.Minute)},
			{Summary: "Soccer practice", Calendar: "calendar.school", Start: at.Add(233 * time.Minute), End: at.Add(293 * time.Minute)},
			{Summary: "Trash day", Calendar: "calendar.waste", Start: time.Date(2026, 9, 17, 0, 0, 0, 0, time.Local), End: time.Date(2026, 9, 18, 0, 0, 0, 0, time.Local), AllDay: true},
		},
		places: worldPlaces(nil),
		// Three calendars, so the Dashboard and the Agenda show their dots in three colors.
		calOrder: []string{"calendar.family", "calendar.school", "calendar.waste"},
	}
	named := facts
	named.named = "Binary"
	later := facts
	later.next = append([]hass.Event{{Summary: "Doctor's appointment", Calendar: "calendar.family", Start: time.Date(2026, 9, 17, 9, 45, 0, 0, time.Local), End: time.Date(2026, 9, 17, 10, 45, 0, 0, time.Local)},
		{Summary: "Still going", Calendar: "calendar.school", Start: at.Add(-30 * time.Minute), End: at.Add(time.Hour)}}, facts.next[:1]...)
	running := []timer.Countdown{{Name: "Pasta", Left: 4*time.Minute + 32*time.Second, Total: 10 * time.Minute, Active: true}}
	chips := []home.Chip{{Icon: "mdi:door", Text: "Back door open"}, {Icon: "mdi:thermometer", Text: "Upstairs 74°"}}
	scenes := map[string]scene{
		"":        {now: at, phase: "idle", weather: sky, style: facts},
		"-named":  {now: at, phase: "idle", weather: sky, style: named},
		"-24h":    {now: at, phase: "idle", weather: sky, style: facts},
		"-timer":  {now: at, phase: "idle", weather: sky, style: facts, timers: running},
		"-glance": {now: at, phase: "idle", weather: sky, style: facts, glance: chips},
		"-strip": {now: at, phase: "idle", weather: sky, style: facts, strip: true, playing: true,
			radio: home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy", Artist: "Eagles"}},
		"-strip-glance": {now: at, phase: "idle", weather: sky, style: facts, glance: chips, strip: true, playing: true,
			radio: home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy", Artist: "Eagles"}},
		"-dawn": {now: time.Date(2026, 9, 16, 5, 30, 0, 0, time.Local), phase: "idle", weather: sky, style: facts},
		// The next alarm makes the date line longer; an event tomorrow at a time makes the when longer.
		"-alarm": {now: at, phase: "idle", weather: sky, style: later, alarms: alarm.View{Next: &alarm.Upcoming{At: at.Add(16 * time.Hour)}}},
		"-strip-timer": {now: at, phase: "idle", weather: sky, style: facts, timers: running, strip: true, playing: true,
			radio: home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy", Artist: "Eagles"}},
	}
	dir := os.Getenv("SHOW_PREVIEW")
	for _, st := range clockStyles {
		if err := config.Set().Screen().ClockStyle(st.value); err != nil {
			t.Fatal(err)
		}
		for suffix, s := range scenes {
			clock24.Store(suffix == "-24h")
			for _, panel := range []struct {
				name       string
				wide, high int
			}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}} {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				newRenderer(img).draw(s)
				if dir == "" {
					continue
				}
				name := "style-" + st.label + suffix + panel.name + ".png"
				f, err := os.Create(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, img); err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
		}
	}
	clock24.Store(false)
}

// The words style says the time in the screen's language; the rules themselves are locale's.
func TestClockWordsFollowTheScreen(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	at := time.Date(2026, 9, 16, 18, 28, 0, 0, time.Local)
	for _, c := range []struct{ lang, said string }{
		{"", "it's twenty-eight past six in the evening"},
		{"de", "es ist kurz vor halb sieben abends"},
		{"es", "son las seis y veintiocho de la tarde"},
		{"ja", "it's twenty-eight past six in the evening"},
	} {
		if err := config.Set().Screen().Language(c.lang); err != nil {
			t.Fatal(err)
		}
		w := clockWords(at)
		parts := []string{w.Intro, w.Lead(), w.Hour, w.Period}
		if w.HourFirst {
			parts = []string{w.Intro, w.Hour, w.Lead(), w.Period}
		}
		if got := strings.Join(parts, " "); got != c.said {
			t.Errorf("%q: %q, want %q", c.lang, got, c.said)
		}
	}
}

// The Spanish hour-first layout at a long time - "son las / cuatro / y cuarenta y tres de la
// madrugada" - which is what makes it shrink and split the part of the day; with SHOW_PREVIEW set,
// each panel's is written there.
func TestShowClockWordsSpanishPreview(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	t.Cleanup(func() {
		_ = config.Set().Screen().ClockStyle("")
		_ = config.Set().Screen().Language("")
	})
	if err := config.Set().Screen().ClockStyle("words"); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Screen().Language("es"); err != nil {
		t.Fatal(err)
	}
	s := scene{now: time.Date(2026, 9, 16, 4, 43, 0, 0, time.Local), phase: "idle"}
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}} {
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		newRenderer(img).draw(s)
		goldenSave(t, "style-Words-es"+panel.name, img)
	}
}

// The Dashboard and the Agenda put each event's calendar color beside it (#667): a calendar with no
// color from Home Assistant takes ours by its place among the calendars shown, as in the calendar, so
// two calendars' events have two colors.
func TestStyleEventColorByCalendar(t *testing.T) {
	f := styleFacts{calOrder: []string{"calendar.family", "calendar.school"}}
	family := f.eventColor(hass.Event{Calendar: "calendar.family"})
	school := f.eventColor(hass.Event{Calendar: "calendar.school"})
	if family != calendarColors[0] || school != calendarColors[1] {
		t.Fatalf("the dots are %v and %v, want %v and %v", family, school, calendarColors[0], calendarColors[1])
	}
}
