//go:build !dot && !spot

package display

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The Dashboard style with a week of events, and with none, on a 12-hour clock and under a timer, draws
// in every screen language on both panels; with SHOW_PREVIEW set, each is written there (dashboard-week-es-show8.png and so on) to look at.
func TestDashboardWeekDraws(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().ClockStyle(styleDashboard); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set().Screen().ClockStyle("") })
	clock24.Store(true)
	t.Cleanup(func() { clock24.Store(false) })
	now := time.Date(2026, 10, 8, 18, 42, 0, 0, time.Local)
	day := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, time.Local) }
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "sunny", "rainy", "cloudy", "sunny"} {
		week = append(week, hass.Day{When: now.AddDate(0, 0, i), Condition: c, High: float64(31 - i), Low: float64(21 - i)})
	}
	s := scene{now: now, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "27°"}, style: styleFacts{
		days: week,
		next: []hass.Event{
			{Summary: "Dinner", Start: day(8, 19, 30), End: day(8, 21, 0)},
			{Summary: "Team meeting", Start: day(9, 10, 0), End: day(9, 11, 0)},
			{Summary: "Family barbecue", Start: day(11, 0, 0), End: day(12, 0, 0), AllDay: true},
			{Summary: "Dentist", Start: day(13, 16, 0), End: day(13, 17, 0)},
			{Summary: "Birthday party", Start: day(14, 0, 0), End: day(15, 0, 0), AllDay: true},
			{Summary: "Piano lesson", Start: day(15, 17, 30), End: day(15, 18, 30)},
			{Summary: "Book club", Start: day(16, 20, 0), End: day(16, 22, 0)},
			{Summary: "Car service", Start: day(19, 9, 0), End: day(19, 10, 0)},
		},
	}}
	empty := s
	empty.style.next = nil
	withTimer := s
	withTimer.timers = []timer.Countdown{{Name: "Pasta", Left: 4 * time.Minute, Total: 10 * time.Minute, Active: true}}
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, lang := range []string{"", "de", "es", "fr", "it", "nl", "pt", "ru"} {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		for _, p := range []struct {
			name       string
			scene      scene
			wide, high int
			h12        bool
		}{{"week", s, showWide, showHigh, false}, {"week", s, show8Wide, show8High, false}, {"week", s, skylightWide, skylightHigh, false}, {"empty", empty, showWide, showHigh, false},
			{"week-12h", s, showWide, showHigh, true}, {"week-12h", s, show8Wide, show8High, true}, {"week-12h", s, skylightWide, skylightHigh, true},
			{"timer", withTimer, showWide, showHigh, false}, {"timer", withTimer, show8Wide, show8High, false}, {"timer", withTimer, skylightWide, skylightHigh, false}} {
			clock24.Store(!p.h12)
			img := image.NewRGBA(image.Rect(0, 0, p.wide, p.high))
			newRenderer(img).draw(p.scene)
			dir := os.Getenv("SHOW_PREVIEW")
			if dir == "" {
				continue
			}
			name := "dashboard-" + p.name
			if lang != "" {
				name += "-" + lang
			}
			switch p.wide {
			case show8Wide:
				name += "-show8"
			case skylightWide:
				name += "-skylight"
			}
			f, err := os.Create(filepath.Join(dir, name+".png"))
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

// dashScene is the Dashboard at a time, with a week of sunny days unless days is false, the
// temperature now when temp is set, and the events given.
func dashScene(at time.Time, temp string, days bool, next []hass.Event) scene {
	var week []hass.Day
	for i := range 5 {
		if days {
			week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: "sunny", High: 86, Low: 64})
		}
	}
	var sky home.Weather
	if temp != "" {
		sky = home.Weather{Condition: "partlycloudy", Temp: temp}
	}
	return scene{now: at, phase: "idle", weather: sky, style: styleFacts{kind: styleDashboard, chosen: true, days: week, next: next}}
}

// dashTaps is where the last frame put the temperature now, the rest of the weather, and the events.
func dashTaps(r *renderer) (temp, weather, next image.Rectangle) {
	r.weatherMu.Lock()
	defer r.weatherMu.Unlock()
	return r.tempAt, r.weatherAt, r.nextAt
}

// dashEvents is n events a day apart, the first from days after at: all-day ones, or at ten.
func dashEvents(at time.Time, from, n int, allDay bool) []hass.Event {
	var out []hass.Event
	for i := range n {
		d := time.Date(at.Year(), at.Month(), at.Day()+from+i, 0, 0, 0, 0, time.Local)
		e := hass.Event{Summary: "Event", Start: d, End: d.AddDate(0, 0, 1), AllDay: true}
		if !allDay {
			e = hass.Event{Summary: "Event", Start: d.Add(10 * time.Hour), End: d.Add(11 * time.Hour)}
		}
		out = append(out, e)
	}
	return out
}

var dashSizes = []image.Point{{showWide, showHigh}, {show8Wide, show8High}, {skylightWide, skylightHigh}}

// The temperature now keeps one place all day and for any reading: on a 12-hour clock it does not
// jump when the hour gains a digit, and a three-figure reading sits where a two-figure one does.
func TestDashboardTemperatureStaysPut(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { clock24.Store(false) })
	for _, h24 := range []bool{false, true} {
		clock24.Store(h24)
		for _, size := range dashSizes {
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
			var first image.Rectangle
			for _, hm := range [][2]int{{21, 48}, {22, 48}, {1, 0}, {12, 59}} {
				for _, temp := range []string{"72°", "104°", "-5°"} {
					r.draw(dashScene(time.Date(2026, 10, 6, hm[0], hm[1], 0, 0, time.Local), temp, true, nil))
					at, _, _ := dashTaps(r)
					switch {
					case at.Empty():
						t.Fatalf("%v 24h %v %02d:%02d %s: no temperature drawn", size, h24, hm[0], hm[1], temp)
					case first.Empty():
						first = at
					case at != first:
						t.Errorf("%v 24h %v %02d:%02d %s: the temperature is at %v, not %v", size, h24, hm[0], hm[1], temp, at, first)
					}
				}
			}
		}
	}
}

// Without room for the coming days, the weather is the line under the date, which has the temperature
// in it: the temperature now is not drawn a second time.
func TestDashboardTemperatureOnce(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 10, 6, 9, 57, 0, 0, time.Local)
	withTimer := dashScene(at, "72°", true, nil)
	withTimer.timers = []timer.Countdown{{Name: "Pasta", Left: 4 * time.Minute, Total: 10 * time.Minute, Active: true}}
	for name, c := range map[string]struct {
		s    scene
		size image.Point
	}{
		"a timer on a Show 5":     {withTimer, image.Pt(showWide, showHigh)},
		"no forecast on a Show 5": {dashScene(at, "72°", false, nil), image.Pt(showWide, showHigh)},
		"no forecast on a Show 8": {dashScene(at, "72°", false, nil), image.Pt(show8Wide, show8High)},
	} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, c.size.X, c.size.Y)))
		r.draw(c.s)
		temp, weather, _ := dashTaps(r)
		if !temp.Empty() {
			t.Errorf("%s: the temperature is drawn at %v as well as on the weather line", name, temp)
		}
		if weather.Empty() {
			t.Errorf("%s: no weather line", name)
		}
	}
}

// The agenda's tap area starts right of the middle of the screen, where a tap starts Assist, whether
// the weather is known or not; and a tap on the temperature opens the forecast, wherever it sits.
func TestDashboardTapsLeaveTheMiddle(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { clock24.Store(false) })
	at := time.Date(2026, 10, 6, 21, 48, 0, 0, time.Local)
	for _, h24 := range []bool{false, true} {
		clock24.Store(h24)
		for _, size := range dashSizes {
			for _, temp := range []string{"", "72°"} {
				r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
				r.draw(dashScene(at, temp, true, dashEvents(at, 1, 3, false)))
				tempAt, _, next := dashTaps(r)
				if next.Min.X <= r.w/2 {
					t.Errorf("%v 24h %v temp %q: the agenda's tap area starts at %d, left of the middle %d", size, h24, temp, next.Min.X, r.w/2)
				}
				c := image.Pt((tempAt.Min.X+tempAt.Max.X)/2, (tempAt.Min.Y+tempAt.Max.Y)/2)
				if temp != "" && !r.weatherTapped(c) {
					t.Errorf("%v 24h %v: a tap on the temperature at %v does not open the forecast", size, h24, c)
				}
			}
		}
	}
}

// Without the coming days' row, the divider still runs down beside the last event drawn.
func TestDashboardDividerBesideTheEvents(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 10, 6, 9, 57, 0, 0, time.Local)
	withTimer := dashScene(at, "72°", true, dashEvents(at, 1, 8, false))
	withTimer.timers = []timer.Countdown{{Name: "Pasta", Left: 4 * time.Minute, Total: 10 * time.Minute, Active: true}}
	for _, size := range dashSizes {
		for name, s := range map[string]scene{"a timer": withTimer, "no forecast": dashScene(at, "72°", false, dashEvents(at, 1, 8, false))} {
			img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
			r := newRenderer(img)
			r.draw(s)
			_, _, next := dashTaps(r)
			x := next.Min.X + r.s(20) - r.s(30) // the divider is 30 short of the column, which starts 20 into the tap area
			y := next.Max.Y - r.s(10) - r.s(24) // the last event's line, a little above its baseline
			if img.At(x, y) == img.At(x-r.s(8), y) {
				t.Errorf("%v with %s: no divider at %d,%d beside the last event", size, name, x, y)
			}
		}
	}
}

// Events past the ones that fit change nothing drawn: the names column is sized from those shown.
func TestDashboardHiddenEventsChangeNothing(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 10, 6, 9, 57, 0, 0, time.Local)
	for size, fit := range map[image.Point]int{{showWide, showHigh}: 4, {show8Wide, show8High}: 7} {
		draw := func(next []hass.Event) []byte {
			img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
			newRenderer(img).draw(dashScene(at, "72°", true, next))
			return img.Pix
		}
		shown := dashEvents(at, 1, fit, true)                                                       // all-day: short days
		more := append(append([]hass.Event(nil), shown...), dashEvents(at, 1+fit, 8-fit, false)...) // at ten: wider
		if !bytes.Equal(draw(shown), draw(more)) {
			t.Errorf("%v: events that don't fit changed what is drawn", size)
		}
	}
}

// A 12-hour time loses only its AM/PM, and a 24-hour one stays as it is: the Dashboard drops the
// suffix when a long "when" in another language would squeeze the event names.
func TestDashBareTime(t *testing.T) {
	for in, want := range map[string]string{"11:30 PM": "11:30", "9:05 AM": "9:05", "23:30": "23:30", "": ""} {
		if got := dashBareTime(in); got != want {
			t.Errorf("dashBareTime(%q) = %q, want %q", in, got, want)
		}
	}
}
