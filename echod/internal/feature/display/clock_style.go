//go:build !dot

package display

import (
	"log/slog"
	"slices"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// How the home screen's clock looks all day, on the Show and the Spot alike: the classic face, or one
// of seven others (render_styles.go, render_styles_spot.go). Each draws only the time and what goes
// with it; the weather corner, the alert badge, running timers, the music and glance strips and the
// call button stay where they are, whatever the style. The night clock keeps its own look.

const (
	styleClassic   = ""
	styleBig       = "big"
	styleFlip      = "flip"
	styleLED       = "led"
	styleAnalog    = "analog"
	styleWords     = "words"
	styleSun       = "sun"
	styleDashboard = "dashboard"
)

// clockStyles are the choices, as the screen and Home Assistant name them, with what the config keeps.
var clockStyles = []struct {
	label, value string
}{
	{"Classic", styleClassic},
	{"Big", styleBig},
	{"Flip", styleFlip},
	{"LED", styleLED},
	{"Analog", styleAnalog},
	{"Words", styleWords},
	{"Sun", styleSun},
	{"Dashboard", styleDashboard},
}

func clockStyleIndex() int {
	v := config.Get().Screen.ClockStyle
	for i, s := range clockStyles {
		if s.value == v {
			return i
		}
	}
	return 0
}

// clockStyle is the style in force; an unknown one, from a newer build, is the classic face.
func clockStyle() string { return clockStyles[clockStyleIndex()].value }

func clockStyleOptions() []string {
	out := make([]string, len(clockStyles))
	for i, s := range clockStyles {
		out[i] = s.label
	}
	return out
}

// clockStyleRow is the setting on the screen, under Display.
func clockStyleRow() settingRow {
	return settingRow{id: "clockstyle", label: "Clock style", sub: "How the clock looks all day", kind: ctlChoice,
		value: clockStyles[clockStyleIndex()].label}
}

func clockStylePicker() (pickerView, bool) {
	return pickerView{title: "Clock style", opts: clockStyleOptions(), cur: clockStyleIndex()}, true
}

// setClockStyle saves the clock's look and shows it at once, so it can be chosen while looking.
func (d *Display) setClockStyle(i int) {
	if i < 0 || i >= len(clockStyles) {
		return
	}
	if err := config.Set().Screen().ClockStyle(clockStyles[i].value); err != nil {
		slog.Error("saving the clock style failed", "err", err)
		return
	}
	if d.clockStyleSel != nil {
		d.clockStyleSel.Set(clockStyles[i].label)
	}
	d.wake()
}

// clockStyleSelect is Clock style in Home Assistant.
func clockStyleSelect(d *Display) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_clock_style",
			Name:     "Clock style",
			Icon:     "mdi:clock-outline",
			Category: esphome.CategoryConfig,
		},
		Options: clockStyleOptions(),
	}
	s.OnCommand = func(v string) {
		for i, o := range clockStyles {
			if o.label == v {
				d.setClockStyle(i)
				return
			}
		}
	}
	return s
}

// styleFacts is what a clock style shows beyond the time and the weather: the day's sunrise and sunset
// for the Sun, the coming days and the next events for the Dashboard. Gathered only for the style in
// force, and never by asking anything: each comes from what the home feature already keeps.
type styleFacts struct {
	// kind is the style this frame is drawn in, read once with the rest so a change arriving part way
	// through a frame cannot draw one style with another's facts; chosen says it was set at all (a
	// preview built by hand leaves it, and the style in force is read instead).
	kind   string
	chosen bool

	rise, set time.Time
	sunOK     bool
	days      []hass.Day
	next      []hass.Event
}

func styleFactsFor(style string, now time.Time) (f styleFacts) {
	f.kind, f.chosen = style, true
	switch style {
	case styleSun:
		f.rise, f.set, f.sunOK = home.Get().SunTimes(now)
	case styleDashboard:
		f.days = home.Get().Forecast()
		f.next = upcomingEvents(now, 3)
	}
	return f
}

// style is the frame's clock style: the one the facts were gathered for, or the one in force.
func (f styleFacts) style() string {
	if f.chosen {
		return f.kind
	}
	return clockStyle()
}

// upcomingDays is how far ahead the Dashboard looks for its next events, today the first.
const upcomingDays = 7

// upcomingEvents is up to n of the coming week's events that have not ended, soonest first.
func upcomingEvents(now time.Time, n int) []hass.Event {
	var out []hass.Event
	for d := range upcomingDays {
		events, _ := home.Get().EventsOn(now.AddDate(0, 0, d))
		for _, e := range events {
			if e.End.After(now) && !slices.ContainsFunc(out, func(o hass.Event) bool { return o.Summary == e.Summary && o.Start.Equal(e.Start) }) {
				out = append(out, e)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b hass.Event) int { return a.Start.Compare(b.Start) })
	return out[:min(len(out), n)]
}

// numberWords are the numbers the words style says.
var numberWords = []string{"", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
	"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen", "twenty"}

func numberWord(n int) string {
	if n <= 20 {
		return numberWords[n]
	}
	return "twenty-" + numberWords[n-20]
}

// clockWords is the time as it is said: what comes before the hour ("seven past", "quarter to", empty
// on the hour), the hour ("two", "two o'clock", "noon", "midnight"), and the part of the day.
func clockWords(t time.Time) (lead, hour, period string) {
	h, m := t.Hour(), t.Minute()
	switch {
	case m == 0:
	case m == 1:
		lead = "a minute past"
	case m == 15:
		lead = "quarter past"
	case m == 30:
		lead = "half past"
	case m == 45:
		lead = "quarter to"
	case m == 59:
		lead = "a minute to"
	case m < 30:
		lead = numberWord(m) + " past"
	default:
		lead = numberWord(60-m) + " to"
	}
	said := h
	if m > 30 {
		said = (h + 1) % 24
	}
	// The part of the day is the said hour's - "quarter to five in the morning", not "at night" - except
	// for noon and midnight, which belong to the hour before them.
	ph := said
	if said == 12 || said == 0 {
		ph = h
	}
	switch {
	case said == 0 && m == 0:
		return "", "midnight", ""
	case said == 12 && m == 0:
		return "", "noon", ""
	}
	hour = numberWords[(said+11)%12+1]
	if m == 0 {
		hour += " o'clock"
	}
	switch {
	case ph >= 5 && ph < 12:
		period = "in the morning"
	case ph >= 12 && ph < 18:
		period = "in the afternoon"
	case ph >= 18 && ph < 22:
		period = "in the evening"
	default:
		period = "at night"
	}
	return lead, hour, period
}
