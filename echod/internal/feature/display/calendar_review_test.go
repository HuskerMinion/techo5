//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A hidden calendar no longer shown is not hidden, so it cannot come back hidden for good; with one
// calendar shown none is, as there are no chips to show it again. The calendar page, the clock's card
// and the pop-ups all go by that.
func TestHiddenCalendarsKeepToThoseShown(t *testing.T) {
	if got := hiddenAmong([]string{calWork, "calendar.gone", calWork}, lookOrder); !slices.Equal(got, []string{calWork}) {
		t.Errorf("hidden among three: %v", got)
	}
	if got := hiddenAmong([]string{calWork}, []string{calWork}); got != nil {
		t.Errorf("hidden with one calendar shown: %v", got)
	}

	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Calendar().Sources([]string{calFamily})
	_ = config.Set().Calendar().Hidden([]string{calFamily})
	c := config.Get().Calendar
	if got := popupCandidates(lookEvents(), c); len(got) != len(lookEvents()) {
		t.Errorf("with one calendar, %d of %d events may pop up", len(got), len(lookEvents()))
	}
	_ = config.Set().Calendar().Sources(lookOrder)
	_ = config.Set().Calendar().Hidden([]string{calWork})
	for _, e := range popupCandidates(lookEvents(), config.Get().Calendar) {
		if e.Calendar == calWork {
			t.Errorf("a hidden calendar's %q may pop up", e.Summary)
		}
	}

	// The page itself: a hidden calendar the page does not list is shown.
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	v := lookView()
	v.order, v.hidden = []string{calWork}, []string{calWork}
	r.draw(scene{now: lookNow, phase: "idle", showCalendar: true, cal: v})
	if !slices.ContainsFunc(r.calHits, func(h calHit) bool { return h.kind == calEvent && h.ev.Calendar == calWork }) {
		t.Error("the only calendar's events were left hidden")
	}
}

// Many calendars: the chips wrap onto a second row and then end in "+N", every chip on the screen, and
// "+N" opens the list of all of them, where each can be turned over, a hidden one too.
func TestManyCalendarsChips(t *testing.T) {
	for _, n := range []int{6, 12, 15} {
		d, _ := calTestDisplay(t)
		v := lookView()
		v.order, v.names = nil, map[string]string{}
		for i := range n {
			id := fmt.Sprintf("calendar.c%d", i)
			v.order = append(v.order, id)
			v.names[id] = fmt.Sprintf("Calendar number %d", i)
		}
		_ = config.Set().Calendar().Sources(v.order)
		last := v.order[n-1]
		_ = config.Set().Calendar().Hidden([]string{last})
		draw := func() {
			d.mu.Lock()
			v.chipsOpen = d.calChips
			d.mu.Unlock()
			v.hidden = hiddenHere(config.Get().Calendar)
			d.r.draw(scene{now: lookNow, phase: "idle", showCalendar: true, cal: v})
		}
		draw()
		screen := d.r.dst.Rect
		chips, more := 0, false
		for _, h := range d.r.calHits {
			if h.kind == calChip {
				chips++
				if !h.r.Inset(4).In(screen) {
					t.Errorf("%d calendars: a chip off the screen at %v", n, h.r)
				}
			}
			more = more || h.kind == calChipsMore
		}
		if chips+boolInt(more) == 0 || chips < n && !more {
			t.Fatalf("%d calendars: %d chips and no +N", n, chips)
		}
		if !more {
			continue
		}
		p := hitOf(t, d.r, calChipsMore)
		d.calendarGesture(touch.Gesture{Kind: touch.Tap, X: p.X, Y: p.Y})
		draw()
		var hiddenChip image.Point
		listed := 0
		for _, h := range d.r.calHits {
			if h.kind == calChip && h.r.Inset(3).In(screen) {
				listed++
				if h.cal == last {
					hiddenChip = image.Pt((h.r.Min.X+h.r.Max.X)/2, (h.r.Min.Y+h.r.Max.Y)/2)
				}
			}
		}
		if listed < n || hiddenChip == (image.Point{}) {
			t.Fatalf("%d calendars: the list has %d chips on the screen, the hidden one at %v", n, listed, hiddenChip)
		}
		// The list's chips, drawn after its window, each clear of the others.
		inside := slices.IndexFunc(d.r.calHits, func(h calHit) bool { return h.kind == calInside })
		var inList []image.Rectangle
		for _, h := range d.r.calHits[inside:] {
			if h.kind == calChip {
				inList = append(inList, h.r)
			}
		}
		for i := range inList {
			for j := i + 1; j < len(inList); j++ {
				if inList[i].Overlaps(inList[j]) {
					t.Errorf("%d calendars: chips in the list at %v and %v overlap", n, inList[i], inList[j])
				}
			}
		}
		d.calendarGesture(touch.Gesture{Kind: touch.Tap, X: hiddenChip.X, Y: hiddenChip.Y})
		if h := config.Get().Calendar.Hidden; len(h) != 0 || !d.calChips {
			t.Errorf("%d calendars: after a tap in the list, hidden %v and the list up %v", n, h, d.calChips)
		}
		draw()
		for _, h := range d.r.calHits {
			if h.kind == calChipsDone {
				p = image.Pt((h.r.Min.X+h.r.Max.X)/2, (h.r.Min.Y+h.r.Max.Y)/2) // the last: Done itself
			}
		}
		d.calendarGesture(touch.Gesture{Kind: touch.Tap, X: p.X, Y: p.Y})
		if d.calChips {
			t.Errorf("%d calendars: Done left the list up", n)
		}
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Late in the evening the day opens on its last page, and a swipe down moves a whole step.
func TestTimelineLateInTheDay(t *testing.T) {
	start, tl := timelineFor(22, 7.8, 0)
	if start != 24-7.8 || tl.high != 0 {
		t.Errorf("opened at %v with %+v", start, tl)
	}
	moved, _ := timelineFor(22, 7.8, -(tl.shown - 1))
	if got := start - moved; got != float64(tl.shown-1) {
		t.Errorf("a swipe down moved %v hours, want %d", got, tl.shown-1)
	}
	if s, _ := timelineFor(22, 7.8, 100); s != start {
		t.Errorf("scrolled past the end to %v", s)
	}
}

// The card is worked out once a minute and again when the events or its settings change, not on every
// frame.
func TestNextUpIsKept(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Calendar().Sources(lookOrder)
	var k nextUpKept
	k.card(styleClassic, lookNow)
	key := k.key
	k.card(styleClassic, lookNow.Add(20*time.Second))
	if k.key != key {
		t.Error("worked out again within the minute")
	}
	k.card(styleClassic, lookNow.Add(time.Minute))
	if k.key == key {
		t.Error("not worked out again a minute later")
	}
	key = k.key
	_ = config.Set().Calendar().NoNextUp(true)
	if k.card(styleClassic, lookNow.Add(time.Minute)) != nil || k.key == key {
		t.Error("turning the switch off did not take the card away")
	}
	_ = home.Get() // the events' version is in the key too (home.CalendarVersion)
}

// clipText finds the same cut as one letter at a time would, and a title of thousands of letters is cut
// without measuring it thousands of times.
func TestClipTextByHalves(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	oneByOne := func(s string, width int) string {
		if r.width(r.micro, s) <= width {
			return s
		}
		run := []rune(s)
		for len(run) > 0 {
			run = run[:len(run)-1]
			cut := string(run)
			if r.width(r.micro, cut+ellipsis) <= width {
				if i := strings.LastIndex(strings.TrimRight(cut, " "), " "); i > len(cut)-12 && i > 0 {
					cut = cut[:i]
				}
				return strings.TrimRight(cut, " ") + ellipsis
			}
		}
		return ellipsis
	}
	for _, s := range []string{"Parent-teacher conferences for the whole school", "Soccer practice", "Ä", "Quarterly review with everybody"} {
		for w := 0; w < 400; w += 7 {
			if got, want := clipText(r, r.micro, s, w), oneByOne(s, w); got != want {
				t.Errorf("%q in %d: %q, want %q", s, w, got, want)
			}
		}
	}
	long := strings.Repeat("Trash day ", 500)
	start := time.Now()
	got := clipText(r, r.micro, long, 300)
	if r.width(r.micro, got) > 300 || time.Since(start) > 2*time.Second {
		t.Errorf("a long title: %d wide, in %v", r.width(r.micro, got), time.Since(start))
	}
}

// The glance strip's own chips keep the strip: the card only takes its place when it has none.
func TestNextUpLeavesTheGlanceStrip(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: lookNow, phase: "idle", nextUp: lookCard(), glance: []home.Chip{{Icon: "door", Text: "Back door open"}}})
	if day, ok := r.nextCardTapped(image.Pt(r.w/2, r.h-r.s(60))); ok {
		t.Errorf("a card over the glance strip's chips, for %v", day)
	}
	r.draw(scene{now: lookNow, phase: "idle", nextUp: lookCard()})
	if _, ok := r.nextCardTapped(image.Pt(r.w/2, r.h-r.s(60))); !ok {
		t.Error("no card with the strip empty")
	}
}

// An event on since an earlier day does not keep the card from today's; alone it says until when, and
// a start on another day has its weekday.
func TestNextUpPrefersToday(t *testing.T) {
	conf := hass.Event{Summary: "Conference", Calendar: calWork, Start: oct(17, 8, 0), End: oct(19, 18, 0)}
	c, ok := pickNextUp(append(lookEvents(), conf), lookNow)
	if !ok || c.ev.Summary != "Pumpkin patch" {
		t.Errorf("with a conference on since yesterday: %+v", c.ev)
	}
	c, ok = pickNextUp([]hass.Event{conf}, lookNow)
	if !ok || c.ev.Summary != "Conference" {
		t.Fatalf("the conference alone: %+v %v", c.ev, ok)
	}
	if got := nextWhen(c.ev, lookNow); got != "until Mon "+clockText(oct(19, 18, 0)) {
		t.Errorf("the conference says %q", got)
	}
	tomorrow := hass.Event{Summary: "Team standup", Start: oct(19, 7, 0), End: oct(19, 7, 30)}
	if got := nextWhen(tomorrow, oct(18, 22, 0)); got != "Mon "+clockText(tomorrow.Start) {
		t.Errorf("tomorrow's says %q", got)
	}
	if got := nextWhen(lookEvents()[0], lookNow); got != clockText(oct(18, 10, 0)) {
		t.Errorf("today's says %q", got)
	}
}

// The week: a timed event across midnight is on the hours of its days and not in the all-day strip as
// well; the hours stay within an hour of 7 AM to 9 PM, with what is further out a marker that opens
// the day; and a short event is still there to see and tap.
func TestWeekOvernightAndShortEvents(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	night := hass.Event{Summary: "Night shift", Calendar: calWork, Start: oct(20, 20, 0), End: oct(21, 4, 0)}
	quick := hass.Event{Summary: "Call", Calendar: calWork, Start: oct(22, 12, 0), End: oct(22, 12, 5)}
	late := hass.Event{Summary: "Meteor shower", Calendar: calFamily, Start: oct(23, 23, 30), End: oct(23, 23, 59)}
	events := []hass.Event{night, quick, late}
	if s, e := weekHours(events, octDay(18)); s != 6 || e != 22 {
		t.Errorf("the week's hours %d to %d, want 6 to 22", s, e)
	}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	v := lookView()
	v.events, v.day, v.week = events, octDay(18), true
	r.draw(scene{now: lookNow, phase: "idle", showCalendar: true, cal: v})
	var boxes []image.Rectangle
	var short image.Rectangle
	markers := 0
	for _, h := range r.calHits {
		switch {
		case h.kind == calEvent && h.ev.Summary == "Night shift":
			boxes = append(boxes, h.r)
		case h.kind == calEvent && h.ev.Summary == "Call":
			short = h.r
		case h.kind == calDay && h.r.Dy() == r.s(minBlockPx):
			markers++
		}
	}
	if len(boxes) != 1 {
		t.Errorf("the night shift drawn %d times as a box: %v", len(boxes), boxes)
	}
	if markers != 2 {
		t.Errorf("%d markers, want the night shift's early hours and the meteor shower", markers)
	}
	if short.Dy() < r.s(minBlockPx) {
		t.Errorf("a five-minute call is %v, too small to see or tap", short)
	}
}

// Swipes on the refined Coming up move a page of its rows less one, not the two-line list's.
func TestRefinedAgendaStep(t *testing.T) {
	d, v := calTestDisplay(t)
	d.calAgenda, v.agenda = true, true
	for i := range 14 {
		v.events = append(v.events, hass.Event{Calendar: "calendar.family", Summary: fmt.Sprint("Event ", i),
			Start: v.now.AddDate(0, 0, i/2+1), End: v.now.AddDate(0, 0, i/2+1).Add(time.Hour)})
	}
	d.r.draw(scene{now: v.now, phase: "idle", showCalendar: true, cal: v})
	step := d.r.calendarAgendaStep()
	if step <= d.r.calendarDayRows()-1 {
		t.Fatalf("a step of %d rows, no more than the two-line list's %d", step, d.r.calendarDayRows()-1)
	}
	d.calendarGesture(touch.Gesture{Kind: touch.SwipeUp})
	if d.calScroll != min(step, d.r.calendarAgendaMax()) {
		t.Errorf("a swipe moved %d rows, want %d", d.calScroll, step)
	}
}

// When the clocks go back, an hour that ends at the time it began (1:30 to 1:30 again) is an hour tall.
func TestFallBackEventHasHeight(t *testing.T) {
	chi, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skip("no time zone database:", err)
	}
	from := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC).In(chi) // 1:30 AM CDT
	till := from.Add(time.Hour)                                   // 1:30 AM CST
	if from.Hour() != till.Hour() || from.Minute() != till.Minute() {
		t.Fatalf("%v and %v are not the same on the clock", from, till)
	}
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, chi)
	s0, s1 := blockHours(placed{from: from, till: till}, day)
	if s1-s0 != 1 {
		t.Errorf("the hour drawn from %v to %v", s0, s1)
	}
}

// On the night light the card's countdown is not in the filled accent; the red night clock has no card.
func TestNextUpOnTheNightLight(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	accentIn := func(r *renderer) int {
		box, _ := r.nextUpRect(scene{nextUp: lookCard()})
		n := 0
		for y := box.Min.Y; y < box.Min.Y+box.Dy()/2; y++ {
			for x := box.Max.X - box.Dx()/4; x < box.Max.X; x++ {
				if r.dst.RGBAAt(x, y) == amber {
					n++
				}
			}
		}
		return n
	}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	r.draw(scene{now: lookNow, phase: "idle", nextUp: lookCard()})
	if accentIn(r) == 0 {
		t.Fatal("the day's chip is not in the accent, so this measures nothing")
	}
	r.draw(scene{now: lookNow, phase: "idle", nextUp: lookCard(), nightDim: true})
	if n := accentIn(r); n != 0 {
		t.Errorf("%d pixels of filled accent on the night light", n)
	}
	r.draw(scene{now: lookNow, phase: "idle", nextUp: lookCard(), nightDim: true, redClock: true})
	if _, ok := r.nextCardTapped(image.Pt(r.w/2, r.h-r.s(60))); ok {
		t.Error("a card on the red night clock")
	}
}

// The week's markers are in bands of their own, above and under the hours, and cover no event's box:
// a 4:30 AM flight's marker is not over a 6:00 AM workout.
func TestWeekMarkersCoverNothing(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Calendar().Week(true)
	events := []hass.Event{
		{Summary: "Flight to Denver", Calendar: calWork, Start: oct(20, 4, 30), End: oct(20, 5, 0)},
		{Summary: "Gym", Calendar: calFamily, Start: oct(20, 6, 0), End: oct(20, 7, 0)},
		{Summary: "Stargazing", Calendar: calFamily, Start: oct(20, 23, 0), End: oct(20, 23, 45)},
		{Summary: "Late call", Calendar: calWork, Start: oct(20, 21, 0), End: oct(20, 22, 0)},
	}
	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}, {skylightWide, skylightHigh}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
		v := lookView()
		v.events, v.day, v.week = events, octDay(18), true
		r.draw(scene{now: lookNow, phase: "idle", showCalendar: true, cal: v})
		var markers, boxes []image.Rectangle
		for _, h := range r.calHits {
			switch {
			case h.kind == calDay && h.r.Dy() == r.s(minBlockPx):
				markers = append(markers, h.r)
			case h.kind == calEvent:
				boxes = append(boxes, h.r)
			}
		}
		if len(markers) != 2 {
			t.Fatalf("%v: %d markers, want the flight's and the stargazing's", size, len(markers))
		}
		for _, m := range markers {
			for _, b := range boxes {
				if m.Overlaps(b) {
					t.Errorf("%v: a marker at %v covers a box at %v", size, m, b)
				}
			}
		}
	}
}

// Short events one after the other, drawn taller than they last, stand side by side in the week rather
// than one over the other, each with a tap area of its own.
func TestWeekShortEventsDoNotOverlap(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Calendar().Week(true)
	var events []hass.Event
	for i, name := range []string{"Standup", "Sync", "Review"} {
		events = append(events, hass.Event{Summary: name, Calendar: calWork,
			Start: oct(21, 9, 30*i), End: oct(21, 9, 30*i).Add(30 * time.Minute)})
	}
	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}, {skylightWide, skylightHigh}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
		v := lookView()
		v.events, v.day, v.week = events, octDay(18), true
		r.draw(scene{now: lookNow, phase: "idle", showCalendar: true, cal: v})
		var boxes []image.Rectangle
		for _, h := range r.calHits {
			if h.kind == calEvent {
				boxes = append(boxes, h.r)
				if h.r.Dy() < r.s(minBlockPx) {
					t.Errorf("%v: %q is %v, under a line", size, h.ev.Summary, h.r)
				}
			}
		}
		if len(boxes) != 3 {
			t.Fatalf("%v: %d boxes", size, len(boxes))
		}
		for i := range boxes {
			for j := i + 1; j < len(boxes); j++ {
				if boxes[i].Overlaps(boxes[j]) {
					t.Errorf("%v: %v and %v overlap", size, boxes[i], boxes[j])
				}
			}
		}
	}
	// The day's layout gives them columns too, at the least height they are drawn.
	for _, p := range layoutDay(events, octDay(21), 0.6) {
		if p.cols < 2 {
			t.Errorf("%q alone in its row at a least of 0.6 h", p.ev.Summary)
		}
	}
}

// On the day the clocks go back, an event across the repeated hour is laid out as it is drawn, so one
// starting after it on the clock stands beside it, not under it.
func TestFallBackLayoutAsDrawn(t *testing.T) {
	chi, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skip("no time zone database:", err)
	}
	at := func(h, m int) time.Time { return time.Date(2026, 11, 1, h, m, 0, 0, time.UTC).In(chi) }
	long := hass.Event{Summary: "Night shift end", Start: at(6, 0), End: at(7, 30)} // 1:00 CDT to 1:30 CST
	next := hass.Event{Summary: "Handover", Start: at(8, 0), End: at(9, 0)}         // 2:00 to 3:00 CST
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, chi)
	ps := layoutDay([]hass.Event{long, next}, day, 0)
	if len(ps) != 2 {
		t.Fatalf("%d placed", len(ps))
	}
	if ps[0].s1 <= ps[1].s0 {
		t.Fatalf("drawn %v-%v and %v-%v, which do not overlap, so this tests nothing", ps[0].s0, ps[0].s1, ps[1].s0, ps[1].s1)
	}
	if ps[0].col == ps[1].col {
		t.Errorf("drawn %v-%v and %v-%v in the same column", ps[0].s0, ps[0].s1, ps[1].s0, ps[1].s1)
	}
}

// An event on a hidden calendar and on others shown is drawn as the first of those shown, in the order
// the calendars were chosen: that one's color, not the hidden one's. It is still on the hidden one, for
// what asks which calendars an event is on.
func TestAnEventSharedWithAHiddenCalendarTakesAShownOnesColor(t *testing.T) {
	order := []string{calWork, calFamily, calSchool}
	dinner := hass.Event{Summary: "Dinner", Calendar: calWork, Also: []string{calFamily, calSchool}}
	got := visibleEvents([]hass.Event{dinner}, []string{calWork})
	if len(got) != 1 {
		t.Fatalf("visible: %+v", got)
	}
	e := got[0]
	if e.Calendar != calFamily || calendarColor(order, e.Calendar) != calendarColor(order, calFamily) {
		t.Errorf("drawn as %s's; want the first calendar shown, %s", e.Calendar, calFamily)
	}
	for _, c := range order {
		if !home.OnCalendar(e, c) {
			t.Errorf("no longer on %s", c)
		}
	}
	if dinner.Calendar != calWork || !slices.Equal(dinner.Also, []string{calFamily, calSchool}) {
		t.Errorf("the event kept was changed: %+v", dinner)
	}
	if got := visibleEvents([]hass.Event{dinner}, []string{calWork, calFamily}); len(got) != 1 || got[0].Calendar != calSchool {
		t.Errorf("with two of its calendars hidden: %+v", got)
	}
}

// The months' events are put together with an event in two of them (across the turn of a month) kept
// once, and two events that only look alike kept apart; a few thousand take no time.
func TestTheMonthsEventsAreJoinedOnce(t *testing.T) {
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.Local) }
	trip := hass.Event{Calendar: calFamily, Summary: "Trip", Start: at(30, 9), End: at(30, 9).AddDate(0, 0, 4)}
	var oct, nov []hass.Event
	for i := range 3000 {
		oct = append(oct, hass.Event{Calendar: calWork, Summary: fmt.Sprint("Shift ", i), Start: at(1, 0).Add(time.Duration(i) * time.Minute)})
	}
	oct = append(oct, trip)
	nov = append(nov, trip, hass.Event{Calendar: calSchool, Summary: "Trip", Start: trip.Start})
	start := time.Now()
	got := joinMonths([][]hass.Event{oct, nov})
	if len(got) != 3002 {
		t.Errorf("%d events; want 3,002: the trip once, and the school's own", len(got))
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("joining took %v", took)
	}
}

// Two events with the same calendar, title and start that mergeSame keeps apart, a different end or
// one all day, are kept apart across the months too; two alike in one month are as that month has
// them; and a date past 2262 is told apart by its time as any other.
func TestTheMonthsJoinTellApartWhatAMonthDoes(t *testing.T) {
	at := func(y, d, h int) time.Time { return time.Date(y, 10, d, h, 0, 0, 0, time.Local) }
	short := hass.Event{Calendar: calFamily, Summary: "Trip", Start: at(2026, 30, 9), End: at(2026, 30, 12)}
	long := short
	long.End = at(2026, 30, 9).AddDate(0, 0, 4)
	allDay := short
	allDay.AllDay = true
	oct := []hass.Event{short, long, allDay, short}
	nov := []hass.Event{short, long, allDay}
	if got := joinMonths([][]hass.Event{oct, nov}); len(got) != 4 {
		t.Errorf("%d events; want 4: the three kept apart, and the month's own two alike", len(got))
	}
	far := hass.Event{Calendar: calFamily, Summary: "Far", Start: at(2400, 1, 9), End: at(2400, 1, 10)}
	later := far
	later.Start, later.End = at(2400, 1, 11), at(2400, 1, 12)
	if got := joinMonths([][]hass.Event{{far}, {far, later}}); len(got) != 2 {
		t.Errorf("%d events past 2262; want 2", len(got))
	}
}

// The next-up card over a slideshow photo is the card as it is on the plain ground (drawNextUp): inside
// its rounded corners not a pixel differs, so the filled chip is still walnut on amber and its words
// keep their colors, and round it the photo is as it is with no card, with no patch spilling past its
// corners. A white photo and a gradient, on the usual theme and on Paper, which a photo turns dark
// (onPhoto). With SHOW_PREVIEW set, each is written there to look at.
func TestTheNextUpCardOverAPhotoIsTheCard(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	keepPalette(t)
	dir := os.Getenv("SHOW_PREVIEW")
	white := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	draw.Draw(white, white.Rect, image.White, image.Point{}, draw.Src)
	photos := map[string]*image.RGBA{"white": white, "gradient": gradientPair(showWide, showHigh)}
	for _, theme := range []string{"", "Paper"} {
		if theme != "" {
			applyTheme(themes[themeIndex(theme)])
		}
		frame := func(s scene) (*image.RGBA, *renderer) {
			img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
			r := newRenderer(img)
			r.draw(s)
			return img, r
		}
		var plain *image.RGBA
		var r *renderer
		onPhoto(func() { plain, r = frame(scene{now: lookNow, phase: "idle", nextUp: lookCard()}) })
		box, _ := r.nextUpRect(scene{nextUp: lookCard()})
		inside := box.Inset(r.s(18))
		for name, photo := range photos {
			with, _ := frame(scene{now: lookNow, phase: "idle", nextUp: lookCard(), slideshow: photo})
			without, _ := frame(scene{now: lookNow, phase: "idle", slideshow: photo})
			what := name + " " + theme
			for y := inside.Min.Y; y < inside.Max.Y; y++ {
				for x := inside.Min.X; x < inside.Max.X; x++ {
					if with.RGBAAt(x, y) != plain.RGBAAt(x, y) {
						t.Fatalf("%s: the card over the photo differs at %d,%d: %v, on the plain ground %v", what, x, y, with.RGBAAt(x, y), plain.RGBAAt(x, y))
					}
				}
			}
			// The corners and the ends: above the card's middle is the date's own patch, which sits a little
			// higher with a card under it.
			ring := box.Inset(-r.s(4))
			for _, p := range []image.Point{ring.Min, {ring.Max.X - 1, ring.Min.Y}, {ring.Min.X, ring.Max.Y - 1}, {ring.Max.X - 1, ring.Max.Y - 1},
				{ring.Min.X, box.Min.Y + box.Dy()/2}, {ring.Max.X - 1, box.Min.Y + box.Dy()/2}} {
				if with.RGBAAt(p.X, p.Y) != without.RGBAAt(p.X, p.Y) {
					t.Errorf("%s: by the card at %v the photo is %v, with no card %v", what, p, with.RGBAAt(p.X, p.Y), without.RGBAAt(p.X, p.Y))
				}
			}
			if dir == "" {
				continue
			}
			f, err := os.Create(filepath.Join(dir, "next-up-over-"+name+map[string]string{"": "", "Paper": "-paper"}[theme]+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, with); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
