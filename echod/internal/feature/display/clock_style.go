//go:build !dot

package display

import (
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// How the home screen's clock looks all day, on the Show and the Spot alike: the classic face, or one
// of the others (render_styles.go, render_styles_spot.go, render_faces*.go). Each draws only the time
// and what goes with it; the weather corner, the alert badge, running timers, the music and glance
// strips and the call button stay where they are, whatever the style. The night clock keeps its own
// look. A swipe left or right across the clock turns to the next style or the one before.

const (
	styleClassic   = ""
	styleBig       = "big"
	styleFlip      = "flip"
	styleLED       = "led"
	styleAnalog    = "analog"
	styleWords     = "words"
	styleSun       = "sun"
	styleDashboard = "dashboard"
	styleBinary    = "binary"
	styleWorld     = "world"
	styleAgenda    = "agenda"
	styleGlow      = "glow"
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
	{"Binary", styleBinary},
	{"World", styleWorld},
	{"Agenda", styleAgenda},
	{"Glow", styleGlow},
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

// styleNameFor is how long a swipe's new style says its name.
const styleNameFor = 1500 * time.Millisecond

// stepClockStyle turns to the next style (+1) or the one before (-1), from a swipe across the clock,
// and says its name for a moment.
func (d *Display) stepClockStyle(step int) {
	n := len(clockStyles)
	i := ((clockStyleIndex()+step)%n + n) % n
	d.mu.Lock()
	d.styleNamed, d.styleNamedUntil = clockStyles[i].label, time.Now().Add(styleNameFor)
	d.mu.Unlock()
	slog.Info("screen: clock style by a swipe", "style", clockStyles[i].label)
	d.setClockStyle(i)
	// Once more when the name is due to go, as the clock's next frame might be the second after.
	time.AfterFunc(styleNameFor+50*time.Millisecond, d.wake)
}

// styleName is the name a swipe asked to be shown, until it is due to go.
func (d *Display) styleName(now time.Time) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if now.Before(d.styleNamedUntil) {
		return d.styleNamed
	}
	return ""
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
	// named is the style's name while a swipe that turned to it has it said, else "".
	named string

	rise, set time.Time
	sunOK     bool
	days      []hass.Day
	next      []hass.Event
	places    []worldPlace
	// hadToday is today having had an event that is over now, for the Agenda's "nothing else today".
	hadToday bool
}

func styleFactsFor(style string, now time.Time) (f styleFacts) {
	f.kind, f.chosen = style, true
	switch style {
	case styleSun:
		f.rise, f.set, f.sunOK = home.Get().SunTimes(now)
	case styleDashboard:
		f.days = home.Get().Forecast()
		f.next = upcomingEvents(now, 3)
	case styleAgenda:
		f.next = upcomingEvents(now, 8)
		events, _ := home.Get().EventsOn(now)
		f.hadToday = slices.ContainsFunc(events, func(e hass.Event) bool { return !e.End.After(now) })
	case styleWorld:
		f.places = worldPlaces(config.Get().Screen.WorldClocks)
	}
	return f
}

// worldPlace is one of the World style's clocks: a name and its time zone.
type worldPlace struct {
	name string
	loc  *time.Location
}

// defaultWorld is the World style's places until somebody chooses their own.
var defaultWorld = []string{"America/New_York", "Europe/London", "Asia/Tokyo"}

// maxWorld is how many places the World style has room for.
const maxWorld = 3

// worldZones keeps the time zones once read, since the style asks for them every second.
var worldZones struct {
	mu   sync.Mutex
	locs map[string]*time.Location
}

// worldPlaces is the places named, those whose zone is known, at most maxWorld; none named are the
// style's own.
func worldPlaces(zones []string) []worldPlace {
	if len(zones) == 0 {
		zones = defaultWorld
	}
	worldZones.mu.Lock()
	defer worldZones.mu.Unlock()
	if worldZones.locs == nil {
		worldZones.locs = map[string]*time.Location{}
	}
	var out []worldPlace
	for _, z := range zones {
		loc, seen := worldZones.locs[z]
		if !seen {
			l, err := time.LoadLocation(z)
			if err != nil {
				slog.Warn("screen: a world clock's time zone is not known", "zone", z, "err", err)
			}
			loc, worldZones.locs[z] = l, l
		}
		if loc != nil && len(out) < maxWorld {
			out = append(out, worldPlace{name: placeName(z), loc: loc})
		}
	}
	return out
}

// placeName is a time zone said as a place: "America/New_York" is "New York".
func placeName(zone string) string {
	if i := strings.LastIndexByte(zone, '/'); i >= 0 {
		zone = zone[i+1:]
	}
	return strings.ReplaceAll(zone, "_", " ")
}

// placeDay is how a place's day differs from here: "Tomorrow", "Yesterday", or nothing.
func placeDay(now time.Time, loc *time.Location) string {
	there := now.In(loc)
	a := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	b := time.Date(there.Year(), there.Month(), there.Day(), 0, 0, 0, 0, time.UTC)
	switch {
	case b.After(a):
		return locale.Tomorrow(screenLang())
	case b.Before(a):
		return locale.Yesterday(screenLang())
	}
	return ""
}

// binaryDigits is the time's six digits for the Binary style, the hours as the clock format has them:
// hours, minutes and seconds, tens then ones.
func binaryDigits(now time.Time) [6]int {
	h := now.Hour()
	if clockSuffix(now) != "" {
		h = (h+11)%12 + 1
	}
	m, s := now.Minute(), now.Second()
	return [6]int{h / 10, h % 10, m / 10, m % 10, s / 10, s % 10}
}

// binaryBits is how many lights each of binaryDigits' columns needs: a tens column only ever counts
// to 2 (hours) or 5.
var binaryBits = [6]int{2, 4, 3, 4, 3, 4}

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
