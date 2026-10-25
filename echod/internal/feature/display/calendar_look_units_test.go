//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"path/filepath"
	"slices"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A title's first word with a picture wins, a word matches at the start of the title's words, and a
// title with none of them has none.
func TestKeywordIcons(t *testing.T) {
	for title, want := range map[string]string{
		"Alex's birthday": "cake-variant", "Birthdays at school": "cake-variant", "Trash day": "trash-can",
		"Recycling pickup": "trash-can", "Flight to Denver": "airplane", "Fly to Denver": "airplane",
		"Dentist appointment": "tooth", "Doctor": "stethoscope", "Dr. Lee": "stethoscope",
		"Annual physical": "stethoscope", "Soccer practice": "soccer", "Picture day": "school",
		"Parent-teacher night": "school", "Vacation": "beach", "Holiday party": "beach",
		"Team standup": "", "Butterfly garden": "", "Book club": "",
		// Whole words only, and no word that means too many things alone.
		"Flynn's party": "", "Flyers game": "", "Trashy movie night": "", "Piano practice": "",
		"Hair appointment": "", "Eye appointment": "", "Picture framing": "", "Fly home": "",
	} {
		if got := keywordIcon(title); got != want {
			t.Errorf("%q: %q, want %q", title, got, want)
		}
	}
}

// A countdown is short: now, minutes, hours, or both; counted up to the next minute; nothing once over.
func TestCountdownText(t *testing.T) {
	now := lookNow
	for _, c := range []struct {
		start, end time.Time
		want       string
	}{
		{now.Add(25 * time.Minute), now.Add(time.Hour), "in 25 min"},
		{now.Add(90 * time.Minute), now.Add(2 * time.Hour), "in 1 h 30 min"},
		{now.Add(9 * time.Hour), now.Add(10 * time.Hour), "in 9 h"},
		{now.Add(time.Hour), now.Add(2 * time.Hour), "in 1 h"},
		{now.Add(30 * time.Second), now.Add(time.Hour), "in 1 min"},
		{now.Add(-time.Minute), now.Add(time.Hour), "now"},
		{now.Add(-2 * time.Hour), now.Add(-time.Hour), ""},
	} {
		if got := countdownText(now, c.start, c.end); got != c.want {
			t.Errorf("%v to %v: %q, want %q", c.start.Sub(now), c.end.Sub(now), got, c.want)
		}
	}
	if got := daysCountdown("Vacation", 11); got != "Vacation in 11 days" {
		t.Errorf("%q", got)
	}
	if got := daysCountdown("Vacation", 1); got != "Vacation tomorrow" {
		t.Errorf("%q", got)
	}
}

// The month's start times are a few letters, on a 24-hour clock its own way.
func TestShortClock(t *testing.T) {
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	clock24.Store(false)
	for tm, want := range map[time.Time]string{oct(18, 9, 0): "9a", oct(18, 17, 30): "5:30p", oct(18, 12, 0): "12p", oct(18, 0, 15): "12:15a"} {
		if got := shortClock(tm); got != want {
			t.Errorf("%v: %q, want %q", tm, got, want)
		}
	}
	if got := hourLabel(13); got != "1 PM" {
		t.Errorf("hour label %q", got)
	}
	clock24.Store(true)
	if got := shortClock(oct(18, 17, 30)); got != "17:30" {
		t.Errorf("24-hour %q", got)
	}
	if got := hourLabel(8); got != "08:00" {
		t.Errorf("24-hour hour label %q", got)
	}
}

// Events at the same time stand side by side, as many columns as their group needs; one alone has the
// whole width, and an event running past midnight is kept to the day.
func TestLayoutDayOverlaps(t *testing.T) {
	ev := func(name string, h0, m0, h1, m1 int) hass.Event {
		return hass.Event{Summary: name, Start: oct(21, h0, m0), End: oct(21, h1, m1)}
	}
	got := map[string][2]int{}
	for _, p := range layoutDay([]hass.Event{
		ev("a", 9, 0, 10, 0), ev("b", 9, 30, 11, 0), ev("c", 10, 30, 11, 30), ev("d", 13, 0, 14, 0),
		ev("e", 13, 0, 13, 0), // no length: drawn half an hour long, beside d
		{Summary: "late", Start: oct(21, 23, 0), End: oct(22, 2, 0)},
		{Summary: "all day", Start: octDay(21), End: octDay(22), AllDay: true},
	}, octDay(21), 0) {
		got[p.ev.Summary] = [2]int{p.col, p.cols}
		if p.ev.Summary == "late" && !p.till.Equal(octDay(22)) {
			t.Errorf("an event past midnight ends at %v, not the day's end", p.till)
		}
	}
	want := map[string][2]int{"a": {0, 2}, "b": {1, 2}, "c": {0, 2}, "d": {0, 2}, "e": {1, 2}, "late": {0, 1}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("columns %v, want %v", got, want)
	}
}

// A bar of several days keeps one lane across the week, is cut where it runs on past the week or the
// month, and two bars on the same days take two lanes.
func TestWeekSpans(t *testing.T) {
	trip := hass.Event{Summary: "Trip", Calendar: calFamily, Start: octDay(23), End: octDay(28), AllDay: true} // Fri to Tue
	fair := hass.Event{Summary: "Fair", Calendar: calSchool, Start: octDay(30), End: novDay(2), AllDay: true}  // with the vacation
	events := append(lookEvents(), trip, fair)
	find := func(spans []span, name string) []span {
		var out []span
		for _, sp := range spans {
			if sp.ev.Summary == name {
				out = append(out, sp)
			}
		}
		return out
	}
	week3 := weekSpans(events, octDay(18), 7)
	if s := find(week3, "Trip"); len(s) != 1 || s[0].from != 5 || s[0].to != 6 || s[0].before || !s[0].after {
		t.Errorf("the trip in its first week: %+v", s)
	}
	week4 := weekSpans(events, octDay(25), 7)
	if s := find(week4, "Trip"); len(s) != 1 || s[0].from != 0 || s[0].to != 2 || !s[0].before || s[0].after {
		t.Errorf("the trip in its second week: %+v", s)
	}
	trash, vac := find(week4, "Trash day"), find(week4, "Vacation")
	if len(trash) != 1 || len(vac) != 1 || vac[0].from != 4 || vac[0].to != 6 || !vac[0].after {
		t.Fatalf("the trash and the vacation: %+v %+v", trash, vac)
	}
	if f := find(week4, "Fair"); len(f) != 1 || f[0].lane == vac[0].lane {
		t.Errorf("the fair and the vacation on the same days: %+v %+v", f, vac)
	}
	nov := weekSpans(events, novDay(1), 7)
	if s := find(nov, "Vacation"); len(s) != 1 || s[0].from != 0 || s[0].to != 2 || !s[0].before || s[0].after {
		t.Errorf("the vacation in November: %+v", s)
	}
	if s := find(week3, "Pumpkin patch"); len(s) != 0 {
		t.Error("a timed event of one day became a bar")
	}

	// Drawn: the trip is a bar in each of its two weeks, the vacation one bar to the month's edge.
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
		v := lookView()
		v.events = events
		r.draw(scene{now: lookNow, phase: "idle", showCalendar: true, cal: v})
		var trips, vacs []image.Rectangle
		colW := (r.w - 2*r.margin) / 7
		for _, h := range r.calHits {
			if h.kind == calEvent && h.ev.Summary == "Trip" {
				trips = append(trips, h.r)
			}
			if h.kind == calEvent && h.ev.Summary == "Vacation" {
				vacs = append(vacs, h.r)
			}
		}
		if len(trips) != 2 {
			t.Errorf("%v: the trip drawn %d times, want once in each week", size, len(trips))
		}
		if len(vacs) != 1 || vacs[0].Dx() < 2*colW {
			t.Errorf("%v: the vacation drawn as %v, want one bar across three days", size, vacs)
		}
	}
}

// The day opens an hour before now on today, an hour before its first event on another day; a swipe
// moves it a page less an hour and stops at the day's ends, and the Later pill brings the event into view.
func TestTimelineScroll(t *testing.T) {
	evs := lookEvents()
	if a := autoStart(evs, octDay(18), lookNow); a != 7 {
		t.Errorf("today opens at %v", a)
	}
	if a := autoStart(evs, octDay(21), lookNow); a != 8 {
		t.Errorf("Wednesday opens at %v, an hour before its standup", a)
	}
	if a := autoStart(nil, octDay(26), lookNow); a != 8 {
		t.Errorf("an empty day opens at %v", a)
	}
	start, tl := timelineFor(7, 7.5, 100)
	if start != 16.5 || tl.low != -7 || tl.high != 10 || tl.shown != 7 {
		t.Errorf("scrolled to the end: %v %+v", start, tl)
	}
	if start, _ := timelineFor(7, 7.5, -100); start != 0 {
		t.Errorf("scrolled to the top: %v", start)
	}

	d, _ := calTestDisplay(t)
	v := lookView()
	d.calDay, d.calMonth = octDay(18), octDay(1)
	draw := func() {
		d.mu.Lock()
		v.day, v.scroll = d.calDay, d.calScroll
		d.mu.Unlock()
		d.r.draw(scene{now: lookNow, phase: "idle", showCalendar: true, cal: v})
	}
	draw()
	tl = d.r.calTimeline
	d.calendarGesture(touch.Gesture{Kind: touch.SwipeUp})
	if d.calScroll != tl.shown-1 {
		t.Errorf("a swipe up moved %d hours, want %d", d.calScroll, tl.shown-1)
	}
	for range 10 {
		d.calendarGesture(touch.Gesture{Kind: touch.SwipeUp})
	}
	if d.calScroll != tl.high {
		t.Errorf("swiped to %d, past the day's end at %d", d.calScroll, tl.high)
	}
	d.calScroll = 0
	draw()
	p := hitOf(t, d.r, calLater)
	d.calendarGesture(touch.Gesture{Kind: touch.Tap, X: p.X, Y: p.Y})
	if want := min(17-1-7, tl.high); d.calScroll != want {
		t.Errorf("the Later pill scrolled to %d, want %d", d.calScroll, want)
	}
	draw()
	d.r.calMu.Lock()
	later := slices.ContainsFunc(d.r.calHits, func(h calHit) bool { return h.kind == calLater })
	soccer := slices.ContainsFunc(d.r.calHits, func(h calHit) bool { return h.kind == calEvent && h.ev.Summary == "Soccer practice" })
	d.r.calMu.Unlock()
	if later || !soccer {
		t.Errorf("after the pill, the pill is still there (%v) or soccer is not (%v)", later, soccer)
	}

	// ‹ and › and a swipe move a day, and the hours open again where that day opens.
	d.calendarGesture(touch.Gesture{Kind: touch.SwipeLeft})
	if !sameDay(d.calDay, octDay(19)) || d.calScroll != 0 {
		t.Errorf("a swipe left went to %v at %d", d.calDay, d.calScroll)
	}
	draw()
	p = hitOf(t, d.r, calPrev)
	d.calendarGesture(touch.Gesture{Kind: touch.Tap, X: p.X, Y: p.Y})
	if !sameDay(d.calDay, octDay(18)) {
		t.Errorf("‹ went to %v", d.calDay)
	}
	draw()
	p = hitOf(t, d.r, calNext)
	d.calendarGesture(touch.Gesture{Kind: touch.Tap, X: p.X, Y: p.Y})
	if !sameDay(d.calDay, octDay(19)) {
		t.Errorf("› went to %v", d.calDay)
	}
}

// The chips hide a calendar's events here and show them again; an event on two calendars stays while
// either is shown, and the hidden calendar is this device's own.
func TestCalendarFilter(t *testing.T) {
	both := hass.Event{Summary: "Dinner", Calendar: calWork, Also: []string{calFamily}}
	got := visibleEvents([]hass.Event{{Summary: "Standup", Calendar: calWork}, both, {Summary: "Soccer", Calendar: calSchool}}, []string{calWork})
	if len(got) != 2 || got[0].Summary != "Dinner" {
		t.Errorf("visible: %+v", got)
	}

	d, _ := calTestDisplay(t)
	_ = config.Set().Calendar().Sources(lookOrder)
	v := lookView()
	draw := func() {
		v.hidden = config.Get().Calendar.Hidden
		d.r.draw(scene{now: lookNow, phase: "idle", showCalendar: true, cal: v})
	}
	draw()
	var chip image.Point
	d.r.calMu.Lock()
	n := 0
	for _, h := range d.r.calHits {
		if h.kind == calChip {
			n++
			if h.cal == calWork {
				chip = image.Pt((h.r.Min.X+h.r.Max.X)/2, (h.r.Min.Y+h.r.Max.Y)/2)
			}
		}
	}
	d.r.calMu.Unlock()
	if n != 3 {
		t.Fatalf("%d chips for three calendars", n)
	}
	d.calendarGesture(touch.Gesture{Kind: touch.Tap, X: chip.X, Y: chip.Y})
	if h := config.Get().Calendar.Hidden; !slices.Equal(h, []string{calWork}) {
		t.Fatalf("hidden after a tap: %v", h)
	}
	draw()
	d.r.calMu.Lock()
	for _, h := range d.r.calHits {
		if h.kind == calEvent && h.ev.Calendar == calWork {
			t.Errorf("a hidden calendar's %q is on the month", h.ev.Summary)
		}
	}
	d.r.calMu.Unlock()

	// A settings file neither carries it nor changes it.
	own := config.Get()
	file := own
	file.Calendar.Hidden = []string{calSchool}
	config.KeepOwn(&file, own)
	if !slices.Equal(file.Calendar.Hidden, []string{calWork}) {
		t.Errorf("a restore changed the hidden calendars to %v", file.Calendar.Hidden)
	}
	cleared := own
	config.ClearOwn(&cleared)
	if len(cleared.Calendar.Hidden) != 0 {
		t.Errorf("a file carries the hidden calendars: %v", cleared.Calendar.Hidden)
	}

	d.calendarGesture(touch.Gesture{Kind: touch.Tap, X: chip.X, Y: chip.Y})
	if h := config.Get().Calendar.Hidden; len(h) != 0 {
		t.Errorf("hidden after a second tap: %v", h)
	}

	// One calendar: no chips.
	v.order = lookOrder[:1]
	draw()
	d.r.calMu.Lock()
	defer d.r.calMu.Unlock()
	if slices.ContainsFunc(d.r.calHits, func(h calHit) bool { return h.kind == calChip }) {
		t.Error("chips for a single calendar")
	}
}

// The next-up card: the next event today, or within twelve hours across midnight, the one after it,
// and the days to a long event; none with nothing coming up.
func TestPickNextUp(t *testing.T) {
	c, ok := pickNextUp(lookEvents(), lookNow)
	if !ok || c.ev.Summary != "Pumpkin patch" || c.then == nil || c.then.Summary != "Soccer practice" {
		t.Fatalf("at 8:30: %+v", c)
	}
	if c.big == nil || c.big.Summary != "Vacation" || c.days != 11 {
		t.Errorf("the long event: %+v %d", c.big, c.days)
	}
	if c, ok := pickNextUp(lookEvents(), oct(18, 11, 0)); !ok || c.ev.Summary != "Pumpkin patch" ||
		countdownText(oct(18, 11, 0), c.ev.Start, c.ev.End) != "now" {
		t.Errorf("while the pumpkin patch is on: %+v", c)
	}
	// On Sunday evening Monday's standup at nine is more than twelve hours off; at ten at night it is not.
	if _, ok := pickNextUp(lookEvents(), oct(18, 19, 30)); ok {
		t.Error("a card at 7:30 PM, with nothing for twelve hours")
	}
	if c, ok := pickNextUp(lookEvents(), oct(18, 22, 0)); !ok || c.ev.Summary != "Team standup" {
		t.Errorf("at 10 PM: %+v %v", c, ok)
	}
	if _, ok := pickNextUp(nil, lookNow); ok {
		t.Error("a card with no events")
	}

	// The switch, and the styles that list the events themselves.
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if nextUpFor(styleClassic, lookNow) != nil {
		t.Error("a card with no calendar shown")
	}
	_ = config.Set().Calendar().Sources(lookOrder)
	_ = config.Set().Calendar().NoNextUp(true)
	if nextUpFor(styleClassic, lookNow) != nil {
		t.Error("a card with the switch off")
	}
	_ = config.Set().Calendar().NoNextUp(false)
	if nextUpFor(styleDashboard, lookNow) != nil || nextUpFor(styleAgenda, lookNow) != nil {
		t.Error("a card on a style that lists the events itself")
	}
}

// The card is a tap target opening its event's day, clear of the clock's date; a frame without it
// leaves nothing to tap.
func TestNextUpCardTap(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
		r.draw(scene{now: lookNow, phase: "idle", nextUp: lookCard(), weather: hassSunny()})
		r.weatherMu.Lock()
		card, date := r.nextCardAt, r.dateAt
		r.weatherMu.Unlock()
		if card.Empty() || card.Overlaps(date) {
			t.Fatalf("%v: the card at %v, the date at %v", size, card, date)
		}
		mid := image.Pt(card.Min.X+card.Dx()/2, card.Min.Y+card.Dy()/2)
		day, ok := r.nextCardTapped(mid)
		if !ok || !sameDay(day, octDay(18)) {
			t.Errorf("%v: a tap on the card opens %v %v", size, day, ok)
		}
		r.draw(scene{now: lookNow, phase: "idle", weather: hassSunny()})
		if _, ok := r.nextCardTapped(mid); ok {
			t.Errorf("%v: the card stayed a tap target with none drawn", size)
		}
	}
}

// The two switches that go by the panel's size are on bigger than the Show 5 and off on it, and stay
// unchosen until somebody chooses; the others are on by default. Each one's three ways of being
// changed (Home Assistant, the screen's row, the setup page) change it, and Home Assistant shows it.
func TestCalendarLookDefaults(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	show5, show8 := lookFor(config.Get().Calendar, false), lookFor(config.Get().Calendar, true)
	if show5.week || show5.monthFull || !show8.week || !show8.monthFull {
		t.Errorf("by size: Show 5 %+v, Show 8 %+v", show5, show8)
	}
	for _, l := range []calLook{show5, show8} {
		if !l.timeline || !l.refined || !l.icons || !l.countdowns || !l.nextUp || !l.weather {
			t.Errorf("a switch off by default: %+v", l)
		}
	}
	if !wideScreen(show8Wide, show8High) || wideScreen(showWide, showHigh) || !wideScreen(1920, 1080) {
		t.Error("wideScreen")
	}

	was := panelWide.Load()
	t.Cleanup(func() { panelWide.Store(was) })
	panelWide.Store(true)
	d := &Display{poke: make(chan struct{}, 1)}
	d.buildLookEntities()
	d.lookChanged()
	if c := config.Get().Calendar; c.Week != "" || c.MonthDetails != "" {
		t.Errorf("a default was saved as chosen: %+v", c)
	}
	for i, s := range lookSwitches {
		before := s.get(deviceLook())
		switch e := d.lookEnts[i].(type) {
		case *esphome.Switch:
			if e.Get() != before {
				t.Errorf("%s: Home Assistant shows %v, the screen %v", s.id, e.Get(), before)
			}
			e.OnCommand(!before)
		case *esphome.Select:
			if e.Get() != s.opts[lookOpt(s, deviceLook())] {
				t.Errorf("%s: Home Assistant shows %q", s.id, e.Get())
			}
			e.OnCommand(s.opts[lookOpt(s, deviceLook())^1])
		}
		if s.get(deviceLook()) == before {
			t.Errorf("%s: Home Assistant's command changed nothing", s.id)
		}
		if len(s.opts) > 0 {
			if p, ok := lookPicker(lookRowPrefix + s.id); !ok || len(p.opts) != 2 {
				t.Errorf("%s: its list %+v", s.id, p)
			}
			d.lookChoose(lookRowPrefix+s.id, lookOpt(s, deviceLook())^1)
		} else if !d.lookRowTap(lookRowPrefix + s.id) {
			t.Errorf("%s: its row is not a row", s.id)
		}
		if s.get(deviceLook()) != before {
			t.Errorf("%s: the screen's row did not turn it back", s.id)
		}
	}
	if d.lookRowTap("calpop") || d.lookChoose("theme", 0) {
		t.Error("another row taken for one of the calendar's")
	}
	// The setup page's choices change the same switches.
	for i, c := range d.lookChoices() {
		now := c.Now()
		c.Choose(now ^ 1)
		if c.Now() == now {
			t.Errorf("%s: the setup page's choice changed nothing", lookSwitches[i].id)
		}
	}

	// Saved and read back.
	path := filepath.Join(t.TempDir(), "state.json")
	config.Use(path)
	_ = config.Set().Calendar().Week(false)
	_ = config.Set().Calendar().NoIcons(true)
	config.Use(path)
	if c := config.Get().Calendar; c.Week != config.CalendarOff || !c.NoIcons || lookFor(c, true).week {
		t.Errorf("read back: %+v", c)
	}

	// The rows, under Display, once there is a calendar.
	if len(calendarLookRows()) != 0 {
		t.Error("calendar rows with no calendar shown")
	}
	_ = config.Set().Calendar().Sources(lookOrder)
	if rows := calendarLookRows(); len(rows) != len(lookSwitches)+1 || rows[0].kind != ctlHeading {
		t.Errorf("the rows: %+v", rows)
	}
}

// The week view: its days' headers open the day, whose Done goes back to the week; the switch goes
// between the three views; the arrows and a swipe move a week.
func TestCalendarWeekByTouch(t *testing.T) {
	d, _ := calTestDisplay(t)
	d.r = newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	_ = config.Set().Calendar().Sources(lookOrder)
	v := lookView()
	draw := func() {
		d.mu.Lock()
		v.month, v.day, v.week, v.agenda, v.detail, v.scroll = d.calMonth, d.calDay, d.calWeek, d.calAgenda, d.calDetail, d.calScroll
		d.mu.Unlock()
		v.now = time.Now()
		d.r.draw(scene{now: v.now, phase: "idle", showCalendar: true, cal: v})
	}
	tap := func(k calHitKind) {
		t.Helper()
		draw()
		p := hitOf(t, d.r, k)
		d.calendarGesture(touch.Gesture{Kind: touch.Tap, X: p.X, Y: p.Y})
	}
	d.calMonth = firstOfMonth(time.Now())
	tap(calViewWeek)
	if !d.calWeek || !sameDay(d.calDay, weekStart(time.Now())) {
		t.Fatalf("Week opened %v %v", d.calWeek, d.calDay)
	}
	first := d.calDay
	tap(calNext)
	if !sameDay(d.calDay, first.AddDate(0, 0, 7)) {
		t.Errorf("› on the week went to %v", d.calDay)
	}
	d.calendarGesture(touch.Gesture{Kind: touch.SwipeRight})
	if !sameDay(d.calDay, first) {
		t.Errorf("a swipe right on the week went to %v", d.calDay)
	}
	tap(calDay)
	if d.calWeek || !d.calFromWeek || !sameDay(d.calDay, first) {
		t.Fatalf("a day's header opened %v week=%v", d.calDay, d.calWeek)
	}
	tap(calBack)
	if !d.calWeek || !sameDay(d.calDay, first) {
		t.Fatalf("Done on the day went to %v week=%v", d.calDay, d.calWeek)
	}
	tap(calViewDay)
	if d.calWeek || !sameDay(d.calDay, time.Now()) {
		t.Errorf("Day went to %v week=%v", d.calDay, d.calWeek)
	}
	tap(calBack)
	tap(calMonthView)
	if d.calWeek || !d.calDay.IsZero() || !sameMonth(d.calMonth, first) {
		t.Errorf("Month went to %v %v week=%v", d.calMonth, d.calDay, d.calWeek)
	}
	tap(calViewDay)
	if !sameDay(d.calDay, time.Now()) || d.calFromWeek {
		t.Errorf("Day from the month went to %v", d.calDay)
	}
	tap(calBack)

	// With the week view off there is no switch on the month.
	_ = config.Set().Calendar().Week(false)
	draw()
	d.r.calMu.Lock()
	defer d.r.calMu.Unlock()
	for _, h := range d.r.calHits {
		if h.kind == calViewWeek || h.kind == calViewDay {
			t.Errorf("the switch is on the month with the week view off: %d", h.kind)
		}
	}
}

// The clock's card opens the calendar on its day.
func TestOpenCalendarDay(t *testing.T) {
	d, _ := calTestDisplay(t)
	d.calUntil = time.Time{}
	_ = config.Set().Calendar().Sources(lookOrder)
	if !d.OpenCalendarDay(oct(19, 9, 0)) || !sameDay(d.calDay, octDay(19)) || d.calWeek || d.calMonth.Month() != time.October {
		t.Errorf("opened %v week=%v month %v", d.calDay, d.calWeek, d.calMonth)
	}
}
