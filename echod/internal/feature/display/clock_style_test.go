//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Every clock style draws on both panels, alone, with a timer running, with the glance strip and
// with the music strip; with SHOW_PREVIEW set, each is written there to look at.
func TestShowClockStylesDraw(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().ClockStyle("") })
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
			{Summary: "Dentist", Start: at.Add(83 * time.Minute), End: at.Add(143 * time.Minute)},
			{Summary: "Soccer practice", Start: at.Add(233 * time.Minute), End: at.Add(293 * time.Minute)},
			{Summary: "Trash day", Start: time.Date(2026, 9, 17, 0, 0, 0, 0, time.Local), End: time.Date(2026, 9, 18, 0, 0, 0, 0, time.Local), AllDay: true},
		},
		places: worldPlaces(nil),
	}
	named := facts
	named.named = "Binary"
	later := facts
	later.next = append([]hass.Event{{Summary: "Doctor's appointment", Start: time.Date(2026, 9, 17, 9, 45, 0, 0, time.Local), End: time.Date(2026, 9, 17, 10, 45, 0, 0, time.Local)},
		{Summary: "Still going", Start: at.Add(-30 * time.Minute), End: at.Add(time.Hour)}}, facts.next[:1]...)
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
			}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}} {
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

func TestClockWords(t *testing.T) {
	for _, c := range []struct {
		h, m               int
		lead, hour, period string
	}{
		{14, 7, "seven past", "two", "in the afternoon"},
		{14, 0, "", "two o'clock", "in the afternoon"},
		{14, 15, "quarter past", "two", "in the afternoon"},
		{14, 30, "half past", "two", "in the afternoon"},
		{14, 45, "quarter to", "three", "in the afternoon"},
		{14, 37, "twenty-three to", "three", "in the afternoon"},
		{9, 1, "a minute past", "nine", "in the morning"},
		{11, 59, "a minute to", "twelve", "in the morning"},
		{12, 0, "", "noon", ""},
		{0, 0, "", "midnight", ""},
		{23, 50, "ten to", "twelve", "at night"},
		{19, 20, "twenty past", "seven", "in the evening"},
		{4, 45, "quarter to", "five", "in the morning"}, // the part of the day is the hour said
		{17, 45, "quarter to", "six", "in the evening"},
	} {
		lead, hour, period := clockWords(time.Date(2026, 9, 16, c.h, c.m, 0, 0, time.Local))
		if lead != c.lead || hour != c.hour || period != c.period {
			t.Errorf("%02d:%02d = %q %q %q, want %q %q %q", c.h, c.m, lead, hour, period, c.lead, c.hour, c.period)
		}
	}
}
