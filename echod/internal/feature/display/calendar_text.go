//go:build !dot && !spot

package display

import (
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// What the calendar page works out before it draws: the small picture a title earns, how long until an
// event, which events stand side by side on a day's hours, which run as one bar across a week, the
// day's forecast, and the clock's next event. None of it draws, so all of it can be tested by itself.

// keywordIcons are the small pictures put before an event's title, by words in it: the first entry
// with one of its words or phrases in the title wins, so "Dentist appointment" is the tooth. Each is
// matched as whole words, so "Flyers game" is no flight and "Trashy movie night" no trash day, and a
// word that means too many things alone ("practice", "appointment", "picture") only counts in the
// phrase that pins it down. English only, as the rest of the calendar is; one more is a line here.
var keywordIcons = []struct {
	words []string
	icon  string // a Material Design Icons name (lib/mdi)
}{
	{[]string{"birthday", "birthdays", "bday", "b-day"}, "cake-variant"},
	{[]string{"trash", "trash day", "garbage", "recycling", "recycle"}, "trash-can"},
	{[]string{"flight", "flights", "fly to", "flying", "airport"}, "airplane"},
	{[]string{"dentist", "orthodontist", "dental"}, "tooth"},
	{[]string{"doctor", "doctors", "dr", "checkup", "check-up", "physical", "pediatrician"}, "stethoscope"},
	{[]string{"soccer", "soccer practice", "soccer game"}, "soccer"},
	{[]string{"school", "picture day", "parent-teacher", "parent teacher"}, "school"},
	{[]string{"vacation", "holiday", "holidays"}, "beach"},
}

// keywordPlain is keywordIcons' words as plainWords makes them, each between spaces, worked out once.
var keywordPlain = func() [][]string {
	out := make([][]string, len(keywordIcons))
	for i, k := range keywordIcons {
		for _, w := range k.words {
			out[i] = append(out[i], " "+plainWords(w)+" ")
		}
	}
	return out
}()

// keywordIcon is the picture for a title, or "" when none of its words has one.
func keywordIcon(title string) string {
	words := " " + plainWords(title) + " "
	for i, k := range keywordPlain {
		for _, w := range k {
			if strings.Contains(words, w) {
				return keywordIcons[i].icon
			}
		}
	}
	return ""
}

// plainWords is s in lower case with everything but letters and digits made a single space between
// words, so "Parent-teacher night!" is "parent teacher night".
func plainWords(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}

// countdownText is how long until an event that starts at start and ends at end, as its chip says it:
// "now" while it is on, "in 25 min", "in 1 h 30 min", "in 9 h"; "" once it is over. Minutes are counted
// up, so an event a few seconds away is "in 1 min" until it is "now".
func countdownText(now, start, end time.Time) string {
	if !start.After(now) {
		if end.After(now) || end.Equal(start) && now.Sub(start) < time.Minute {
			return "now"
		}
		return ""
	}
	m := int(math.Ceil(start.Sub(now).Minutes()))
	h, m := m/60, m%60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("in %d h %d min", h, m)
	case h > 0:
		return fmt.Sprintf("in %d h", h)
	}
	return fmt.Sprintf("in %d min", m)
}

// daysCountdown is the countdown to an event starting on another day: "Vacation tomorrow", "Vacation in
// 11 days". days is how many days away it starts, at least one.
func daysCountdown(title string, days int) string {
	if days == 1 {
		return title + " tomorrow"
	}
	return fmt.Sprintf("%s in %d days", title, days)
}

// daysBetween is how many calendar days from a's day to b's, by the dates, whatever the hours.
func daysBetween(a, b time.Time) int {
	da := time.Date(a.Year(), a.Month(), a.Day(), 12, 0, 0, 0, time.Local)
	db := time.Date(b.Year(), b.Month(), b.Day(), 12, 0, 0, 0, time.Local)
	return int(math.Round(db.Sub(da).Hours() / 24))
}

// shortClock is a time in the month's few letters: "9a", "5:30p", or on a 24-hour clock "09:00".
func shortClock(t time.Time) string {
	if clock24.Load() {
		return t.Format("15:04")
	}
	s := t.Format("3")
	if t.Minute() != 0 {
		s = t.Format("3:04")
	}
	if t.Hour() < 12 {
		return s + "a"
	}
	return s + "p"
}

// hourLabel is an hour down the side of the timeline: "8 AM", "12 PM", or "08:00".
func hourLabel(h int) string {
	t := time.Date(2000, 1, 1, h%24, 0, 0, 0, time.Local)
	if clock24.Load() {
		return t.Format("15:04")
	}
	return t.Format("3 PM")
}

// nowLabel is the time beside the now line, without its AM or PM: "8:30", or "08:30".
func nowLabel(t time.Time) string {
	if clock24.Load() {
		return t.Format("15:04")
	}
	return t.Format("3:04")
}

// multiDay is whether an event runs over more than one day: an all-day event of two days or more, or a
// timed one that ends on a later day than it starts (past midnight, a minute after it at least).
func multiDay(e hass.Event) bool {
	if e.AllDay {
		return daysBetween(e.Start, e.End) > 1
	}
	return e.End.After(e.Start) && daysBetween(e.Start, e.End.Add(-time.Minute)) > 0
}

// lastDay is the last day an event is on: an all-day event's end is the day after it.
func lastDay(e hass.Event) time.Time {
	end := e.End
	if e.AllDay || end.After(e.Start) {
		end = end.Add(-time.Minute)
	}
	if end.Before(e.Start) {
		end = e.Start
	}
	return time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.Local)
}

// placed is a timed event on a day's hours: in which of how many columns it stands, those it overlaps
// side by side, the part of it within the day, and the hours it is drawn from and to (blockHours).
type placed struct {
	ev         hass.Event
	col, cols  int
	from, till time.Time // the event's start and end, kept to the day
	s0, s1     float64
}

// minBlock is the shortest an event is drawn on the hours, so one with no length still has a place.
const minBlock = 30 * time.Minute

// layoutDay places a day's timed events on its hours: earliest first, the longer of two starting
// together first, each in the first column free when it starts. Events that overlap, directly or
// through one between them, make a group sharing its columns, each as wide as the group's widest
// row allows; an event alone has the whole width. They are placed as they are drawn: from and to the
// hours blockHours gives them (an hour the clocks went back over is as long as it lasts), and at least
// least hours long, the shortest a box is drawn, so two short events one after the other, drawn
// taller than they last, stand side by side rather than one over the other.
func layoutDay(events []hass.Event, day time.Time, least float64) []placed {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	end := start.AddDate(0, 0, 1)
	var out []placed
	for _, e := range events {
		if e.AllDay || !e.Start.Before(end) || !(e.End.After(start) || e.Start.Equal(start)) {
			continue
		}
		from, till := maxTime(e.Start, start), minTime(e.End, end)
		if till.Sub(from) < minBlock {
			till = minTime(from.Add(minBlock), end)
			from = minTime(from, till.Add(-minBlock))
		}
		p := placed{ev: e, from: from, till: till}
		p.s0, p.s1 = blockHours(p, day)
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(a, b placed) int {
		if a.s0 != b.s0 {
			return cmpFloat(a.s0, b.s0)
		}
		return cmpFloat(b.s1, a.s1)
	})
	drawnTill := func(p placed) float64 { return math.Max(p.s1, p.s0+least) }
	var group []int    // the indexes of the group being placed
	var ends []float64 // when each of its columns is next free
	var groupEnd float64
	finish := func() {
		for _, i := range group {
			out[i].cols = len(ends)
		}
		group, ends = group[:0], ends[:0]
	}
	for i := range out {
		if len(group) > 0 && out[i].s0 >= groupEnd {
			finish()
		}
		till := drawnTill(out[i])
		col := slices.IndexFunc(ends, func(t float64) bool { return t <= out[i].s0 })
		if col < 0 {
			col = len(ends)
			ends = append(ends, till)
		} else {
			ends[col] = till
		}
		out[i].col = col
		group = append(group, i)
		if len(group) == 1 || till > groupEnd {
			groupEnd = till
		}
	}
	finish()
	return out
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// span is an event drawn as one bar across a row of days (a week of the month, or the week view's
// all-day strip): in which lane from the top, from which column to which, and whether it carries on
// before the row's first day or after its last.
type span struct {
	ev            hass.Event
	lane          int
	from, to      int
	before, after bool
}

// weekSpans lays out the bars of a row of n days from first: every all-day event, and every timed one
// that runs past midnight. The earliest first, the longer of two starting together first, each in the
// highest lane free across all its days, so a bar keeps one lane from end to end.
func weekSpans(events []hass.Event, first time.Time, n int) []span {
	first = time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.Local)
	last := first.AddDate(0, 0, n-1)
	var out []span
	for _, e := range events {
		if !e.AllDay && !multiDay(e) {
			continue
		}
		s := time.Date(e.Start.Year(), e.Start.Month(), e.Start.Day(), 0, 0, 0, 0, time.Local)
		l := lastDay(e)
		if l.Before(first) || s.After(last) {
			continue
		}
		sp := span{ev: e, from: max(daysBetween(first, s), 0), to: min(daysBetween(first, l), n-1)}
		sp.before, sp.after = s.Before(first), l.After(last)
		out = append(out, sp)
	}
	slices.SortStableFunc(out, func(a, b span) int {
		if a.from != b.from {
			return a.from - b.from
		}
		if la, lb := a.to-a.from, b.to-b.from; la != lb {
			return lb - la
		}
		return a.ev.Start.Compare(b.ev.Start)
	})
	var taken [][]bool // taken[lane][column]
	for i := range out {
		lane := 0
		for ; lane < len(taken); lane++ {
			if !slices.Contains(taken[lane][out[i].from:out[i].to+1], true) {
				break
			}
		}
		if lane == len(taken) {
			taken = append(taken, make([]bool, n))
		}
		for c := out[i].from; c <= out[i].to; c++ {
			taken[lane][c] = true
		}
		out[i].lane = lane
	}
	return out
}

// dayForecast is the forecast's day for day, if it has one.
func dayForecast(days []hass.Day, day time.Time) (hass.Day, bool) {
	for _, d := range days {
		if !d.When.IsZero() && sameDay(d.When.In(time.Local), day) {
			return d, true
		}
	}
	return hass.Day{}, false
}

// highLow is a forecast day's "58° / 41°".
func highLow(d hass.Day) string { return fmt.Sprintf("%.0f° / %.0f°", d.High, d.Low) }

// weatherGlyph is the small icon for a condition beside a calendar's day (lib/mdi), and its color: the
// sun's gold for sun, the rain's blue for rain and snow, gray clouds for the rest, darker on a light
// theme so they read on its ground.
func weatherGlyph(cond string) (string, color.RGBA) {
	cloud := cloudGray
	if !dark() {
		cloud = cloudDark
	}
	switch cond {
	case "sunny":
		return "weather-sunny", sunGold
	case "clear-night":
		return "weather-night", sunGold
	case "partlycloudy":
		return "weather-partly-cloudy", cloud
	case home.PartlyCloudyNight:
		return "weather-night-partly-cloudy", cloud
	case "rainy":
		return "weather-rainy", rainBlue
	case "pouring":
		return "weather-pouring", rainBlue
	case "lightning":
		return "weather-lightning", cloud
	case "lightning-rainy":
		return "weather-lightning-rainy", rainBlue
	case "snowy":
		return "weather-snowy", rainBlue
	case "snowy-rainy":
		return "weather-snowy-rainy", rainBlue
	case "hail":
		return "weather-hail", rainBlue
	case "fog":
		return "weather-fog", cloud
	case "windy", "windy-variant":
		return "weather-windy", cloud
	case "exceptional":
		return "alert-circle-outline", sunGold
	}
	return "weather-cloudy", cloud
}

// nextUpCard is what the clock's next-up card says: the next event, the one after it, and a longer
// countdown to an event of several days coming up.
type nextUpCard struct {
	ev   hass.Event
	then *hass.Event
	big  *hass.Event // an all-day event of two days or more, starting within bigAhead
	days int         // how many days until big starts
	// color and bigColor are ev's and big's calendars' colors.
	color, bigColor color.RGBA
}

const (
	// nextAhead is how far ahead an event can be and still be next up, when it is not today.
	nextAhead = 12 * time.Hour
	// bigAhead is how far ahead an event of several days is counted down to.
	bigAhead = 30
)

// pickNextUp is the card for now, from the events of the coming days, and whether there is one: only
// while a timed event is on, or starts today or within the next nextAhead. One that starts today or
// later comes first; one on since an earlier day (a conference, a night shift) only has the card while
// nothing else does, so it cannot keep it all day.
func pickNextUp(events []hass.Event, now time.Time) (nextUpCard, bool) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	limit := today.AddDate(0, 0, 1)
	if l := now.Add(nextAhead); l.After(limit) {
		limit = l
	}
	var timed, since []hass.Event // since: on since before today
	var c nextUpCard
	for _, e := range events {
		switch {
		case e.AllDay:
			days := daysBetween(now, e.Start)
			if multiDay(e) && days >= 1 && days <= bigAhead && (c.big == nil || e.Start.Before(c.big.Start)) {
				c.big, c.days = &e, days
			}
		case e.Start.Before(limit) && (e.End.After(now) || !e.End.After(e.Start) && !e.Start.Before(now)):
			if e.Start.Before(today) {
				since = append(since, e)
			} else {
				timed = append(timed, e)
			}
		}
	}
	byStart := func(a, b hass.Event) int { return a.Start.Compare(b.Start) }
	slices.SortStableFunc(timed, byStart)
	slices.SortStableFunc(since, byStart)
	timed = append(timed, since...)
	if len(timed) == 0 {
		return nextUpCard{}, false
	}
	c.ev = timed[0]
	if len(timed) > 1 {
		c.then = &timed[1]
	}
	return c, true
}

// nextWhen is when the card says an event is: its start, with its weekday when that is not today, or
// for one on since an earlier day, "until" its end, with the weekday when that is not today either.
func nextWhen(e hass.Event, now time.Time) string {
	at := func(t time.Time) string {
		if sameDay(t, now) {
			return clockText(t)
		}
		return t.Format("Mon") + " " + clockText(t)
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	if e.Start.Before(today) && e.End.After(now) {
		return "until " + at(e.End)
	}
	return at(e.Start)
}
