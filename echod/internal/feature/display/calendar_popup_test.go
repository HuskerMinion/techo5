//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// When each event pops up: a timed one its lead before it starts until ten minutes in, an all-day one
// from the morning of its first day, only on the calendars chosen, and each only once.
func TestDuePopups(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().Night("22:00-06:30"); err != nil {
		t.Fatal(err)
	}
	at := func(d, h, m int) time.Time { return time.Date(2026, 9, d, h, m, 0, 0, time.Local) }
	dentist := hass.Event{Calendar: "calendar.family", Summary: "Dentist", Start: at(26, 15, 0), End: at(26, 16, 0)}
	birthday := hass.Event{Calendar: "calendar.family", Summary: "Birthday", Start: at(26, 0, 0), End: at(27, 0, 0), AllDay: true}
	trip := hass.Event{Calendar: "calendar.family", Summary: "Trip", Start: at(25, 0, 0), End: at(28, 0, 0), AllDay: true}
	standup := hass.Event{Calendar: "calendar.work", Summary: "Standup", Start: at(26, 15, 5), End: at(26, 15, 20)}
	events := []hass.Event{dentist, birthday, trip, standup}

	c := config.Calendar{Popups: true, Sources: []string{"calendar.family", "calendar.work"}}
	names := func(now time.Time, c config.Calendar, shown map[string]bool) []string {
		var out []string
		for _, e := range duePopups(events, now, c, shown) {
			out = append(out, e.Summary)
		}
		return out
	}
	check := func(what string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: %v, want %v", what, got, want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: %v, want %v", what, got, want)
				return
			}
		}
	}

	check("before the morning", names(at(26, 6, 0), c, nil))
	check("the morning", names(at(26, 6, 30), c, nil), "Birthday") // the trip began yesterday
	check("16 minutes before", names(at(26, 14, 44), c, map[string]bool{popupKey(birthday): true}))
	check("15 minutes before", names(at(26, 14, 45), c, map[string]bool{popupKey(birthday): true}), "Dentist")
	check("both", names(at(26, 14, 50), c, map[string]bool{popupKey(birthday): true}), "Dentist", "Standup")
	check("shown already", names(at(26, 14, 55), c, map[string]bool{popupKey(birthday): true, popupKey(dentist): true}), "Standup")
	check("ten minutes in", names(at(26, 15, 10), c, map[string]bool{popupKey(birthday): true}), "Standup")

	only := c
	only.PopupCalendars = []string{"calendar.work"}
	check("work only", names(at(26, 14, 55), only, nil), "Standup")

	never := c
	never.PopupAllDayNever = true
	check("all-day never", names(at(26, 9, 0), never, nil))

	start := c
	start.PopupBefore = -1
	check("as it starts: a minute before", names(at(26, 14, 59), start, map[string]bool{popupKey(birthday): true}))
	check("as it starts", names(at(26, 15, 0), start, map[string]bool{popupKey(birthday): true}), "Dentist")
}

// The heading's words for how soon.
func TestPopupSoon(t *testing.T) {
	now := time.Date(2026, 9, 26, 14, 45, 0, 0, time.Local)
	e := hass.Event{Start: now.Add(15 * time.Minute), End: now.Add(75 * time.Minute)}
	for _, c := range []struct {
		at   time.Time
		want string
	}{{now, "In 15 minutes"}, {now.Add(14 * time.Minute), "In a minute"}, {now.Add(16 * time.Minute), "Now"}} {
		if got := popupSoon(e, c.at); got != c.want {
			t.Errorf("at %s: %q, want %q", c.at.Format("15:04"), got, c.want)
		}
	}
	if popupSoon(hass.Event{AllDay: true}, now) != "Today" {
		t.Error("all day")
	}
	if got := popupSoon(e, now.Add(80*time.Minute)); got != "Started" {
		t.Errorf("over: %q", got)
	}
}

// The heading is in the screen's language, as the rest of the card is.
func TestPopupSoonIsTranslated(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	if err := config.Set().Screen().Language("fr"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 14, 45, 0, 0, time.Local)
	e := hass.Event{Start: now.Add(15 * time.Minute), End: now.Add(75 * time.Minute)}
	for _, c := range []struct {
		at   time.Time
		want string
	}{{now, "Dans 15 minutes"}, {now.Add(14 * time.Minute), "Dans une minute"}, {now.Add(-45 * time.Minute), "Dans une heure"},
		{now.Add(80 * time.Minute), "Commencé"}} {
		if got := popupSoon(e, c.at); got != c.want {
			t.Errorf("at %s: %q, want %q", c.at.Format("15:04"), got, c.want)
		}
	}
	if got := popupSoon(hass.Event{AllDay: true}, now); got != "Aujourd'hui" {
		t.Errorf("all day: %q", got)
	}
}

// popupTestDisplay is a Show with pop-ups on, and events kept in its month.
func popupTestDisplay(t *testing.T) *Display {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Calendar().Sources([]string{"calendar.family"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Calendar().Popups(true); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Calendar().PopupSilent(true); err != nil {
		t.Fatal(err)
	}
	return &Display{poke: make(chan struct{}, 1)}
}

// Two events at the same time: the second comes up after the first is taken down, and neither pops up
// again - not even after a restart, since what was shown is kept.
func TestPopupsWaitTheirTurn(t *testing.T) {
	d := popupTestDisplay(t)
	at := func(h, m int) time.Time { return time.Date(2026, 9, 26, h, m, 0, 0, time.Local) }
	events := []hass.Event{
		{Calendar: "calendar.family", Summary: "Dentist", Start: at(15, 0), End: at(16, 0)},
		{Calendar: "calendar.family", Summary: "Call Mom", Start: at(15, 0), End: at(15, 30)},
	}
	c := config.Get().Calendar
	now := at(14, 50)

	due := duePopups(events, now, c, popupShownNow())
	if len(due) != 2 {
		t.Fatalf("due %v", due)
	}

	// The queue: one up, the other waiting. Only the one up counts as shown, so a restart now would
	// still bring the second.
	d.popupQueue = due
	d.popupTick(now)
	first := d.popupUp()
	if first == nil {
		t.Fatal("nothing came up from the queue")
	}
	if n := d.popupsWaiting(); n != 1 {
		t.Errorf("%d waiting under the first, want the other one", n)
	}
	if got := duePopups(events, now, config.Get().Calendar, popupShownNow()); len(got) != 1 || got[0].Summary == first.Summary {
		t.Fatalf("still due after the first came up: %v", got)
	}
	d.dismissPopup()
	d.popupTick(now.Add(time.Second))
	second := d.popupUp()
	if second == nil || second.Summary == first.Summary {
		t.Fatalf("after the first, %v", second)
	}
	// Both have come up: neither is due again, even after a restart.
	if got := duePopups(events, now, config.Get().Calendar, popupShownNow()); len(got) != 0 {
		t.Errorf("popped up again: %v", got)
	}
}

// A calendar no longer shown is no longer one that pops up; none left is every one again.
func TestPopupCalendarsFollowTheCalendarsShown(t *testing.T) {
	popupTestDisplay(t)
	if err := config.Set().Calendar().PopupCalendars([]string{"calendar.family"}); err != nil {
		t.Fatal(err)
	}
	if err := setPopupCalendars("calendar.other"); err == nil {
		t.Error("a calendar the device does not show was taken")
	}
	if err := home.Get().SetCalendarSources([]string{"calendar.work"}); err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Calendar.PopupCalendars; got != nil {
		t.Errorf("pop-up calendars %v, want every one (nil)", got)
	}
}

// A tap takes a pop-up down only where one was drawn.
func TestPopupTapFollowsTheDrawing(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480)))
	now := time.Date(2026, 9, 26, 14, 50, 0, 0, time.Local)
	e := hass.Event{Calendar: "calendar.family", Summary: "Dentist", Start: now.Add(10 * time.Minute), End: now.Add(time.Hour)}
	mid := image.Pt(480, 240)
	r.draw(scene{now: now, phase: "idle", popup: &e})
	if !r.popupTapped(mid) {
		t.Error("a tap on the pop-up was missed")
	}
	r.draw(scene{now: now, phase: "idle", popup: &e, showReminder: true}) // under a reminder: not drawn
	if r.popupTapped(mid) {
		t.Error("a tap went to a pop-up hidden under a reminder")
	}
}

// An event shown once for two calendars pops up when either of them is chosen for pop-ups.
func TestAMergedEventPopsUpForEitherCalendar(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	start := time.Date(2026, 9, 26, 15, 0, 0, 0, time.Local)
	dinner := hass.Event{Calendar: "calendar.family", Also: []string{"calendar.work"}, Summary: "Dinner",
		Start: start, End: start.Add(time.Hour)}
	c := config.Calendar{Popups: true, Sources: []string{"calendar.family", "calendar.work"},
		PopupCalendars: []string{"calendar.work"}}
	if due := duePopups([]hass.Event{dinner}, start.Add(-10*time.Minute), c, nil); len(due) != 1 {
		t.Errorf("%d pop-ups, want the dinner, on the work calendar too", len(due))
	}
}

// An event is matched across devices by its start and title, whichever calendar each shows it under.
func TestPopupMatchIgnoresTheCalendar(t *testing.T) {
	start := time.Date(2026, 9, 26, 15, 0, 0, 0, time.Local)
	e := hass.Event{Calendar: "calendar.family", Summary: "Dentist", Start: start}
	other := e
	other.Calendar, other.Also = "calendar.work", []string{"calendar.family"}
	if popupMatch(e) != popupMatch(other) {
		t.Error("the same event under another calendar did not match")
	}
	later := e
	later.Start = start.Add(time.Hour)
	renamed := e
	renamed.Summary = "Dentist again"
	if popupMatch(e) == popupMatch(later) || popupMatch(e) == popupMatch(renamed) {
		t.Error("a different event matched")
	}
	if n := len(popupMatch(e)); n > 64 {
		t.Errorf("match is %d long, more than the link between devices carries", n)
	}
}

// A tap on a pop-up tells the other devices which event it was.
func TestTappingAPopupAwayTellsTheHouse(t *testing.T) {
	d := popupTestDisplay(t)
	sent := make(chan string, 1)
	was := sendPopupDismissed
	sendPopupDismissed = func(id string) { sent <- id }
	t.Cleanup(func() { sendPopupDismissed = was })

	at := func(h, m int) time.Time { return time.Date(2026, 9, 26, h, m, 0, 0, time.Local) }
	e := hass.Event{Calendar: "calendar.family", Summary: "Dentist", Start: at(15, 0), End: at(16, 0)}
	d.popupQueue = []hass.Event{e}
	d.popupTick(at(14, 50))
	if d.popupUp() == nil {
		t.Fatal("nothing came up")
	}
	d.tapPopupAway()
	if d.popupUp() != nil {
		t.Error("the pop-up stayed up")
	}
	select {
	case id := <-sent:
		if id != popupMatch(e) {
			t.Errorf("sent %q, want %q", id, popupMatch(e))
		}
	case <-time.After(time.Second):
		t.Fatal("nothing was sent")
	}

	// With nothing up, a tap sends nothing.
	d.tapPopupAway()
	select {
	case id := <-sent:
		t.Errorf("sent %q with no pop-up up", id)
	case <-time.After(50 * time.Millisecond):
	}
}

// A pop-up tapped away on another device comes down here, whether up or waiting its turn, and does
// not pop up later; another event is left alone.
func TestAPopupTappedAwayElsewhereComesDown(t *testing.T) {
	d := popupTestDisplay(t)
	at := func(h, m int) time.Time { return time.Date(2026, 9, 26, h, m, 0, 0, time.Local) }
	dentist := hass.Event{Calendar: "calendar.family", Summary: "Dentist", Start: at(15, 0), End: at(16, 0)}
	call := hass.Event{Calendar: "calendar.family", Summary: "Call Mom", Start: at(15, 0), End: at(15, 30)}
	school := hass.Event{Calendar: "calendar.family", Summary: "School run", Start: at(15, 5), End: at(15, 30)}
	now := at(14, 50)

	d.popupQueue = []hass.Event{dentist, call, school}
	d.popupTick(now)
	up := d.popupUp()
	if up == nil {
		t.Fatal("nothing came up")
	}

	// The one waiting its turn, tapped away elsewhere, never comes up here.
	var waiting hass.Event
	for _, e := range d.popupQueue {
		if e.Summary != up.Summary {
			waiting = e
			break
		}
	}
	d.popupDismissedElsewhere(popupMatch(waiting), now)
	if d.popupUp() == nil || d.popupUp().Summary != up.Summary {
		t.Errorf("the pop-up up changed to %v", d.popupUp())
	}

	// The one up comes down, and the last one comes up in its place.
	d.popupDismissedElsewhere(popupMatch(*up), now)
	d.popupTick(now.Add(time.Second))
	last := d.popupUp()
	if last == nil || last.Summary == up.Summary || last.Summary == waiting.Summary {
		t.Fatalf("after both were tapped away elsewhere, %v", last)
	}

	// An event tapped away elsewhere before it was due here is kept from popping up later.
	d.dismissPopup()
	d.popupDismissedElsewhere(popupMatch(school), now)
	d.popupQueue = []hass.Event{school}
	d.popupTick(now.Add(time.Minute))
	if got := d.popupUp(); got != nil {
		t.Errorf("%v popped up after it was tapped away elsewhere", got)
	}
}

// A pop-up tapped away on another device stays away after this one restarts: what it heard is kept in
// the config, for a day, and only so many at most (#558).
func TestAPopupTappedAwayElsewhereIsKeptAcrossARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	config.Use(path)
	if err := config.Set().Calendar().Sources([]string{"calendar.family"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Calendar().Popups(true); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Calendar().PopupSilent(true); err != nil {
		t.Fatal(err)
	}
	at := func(h, m int) time.Time { return time.Date(2026, 9, 26, h, m, 0, 0, time.Local) }
	school := hass.Event{Calendar: "calendar.family", Summary: "School run", Start: at(15, 5), End: at(15, 30)}
	now := at(14, 0) // heard of before it is due here
	(&Display{poke: make(chan struct{}, 1)}).popupDismissedElsewhere(popupMatch(school), now)

	// The restart: the config read again from the file, and a display with nothing in memory.
	config.Use(path)
	if got := config.Get().Calendar.PopupGone; len(got) != 1 || got[0].ID != popupMatch(school) || !got[0].At.Equal(now) {
		t.Fatalf("kept after a reload: %+v", got)
	}
	d := &Display{poke: make(chan struct{}, 1)}
	d.popupQueue = []hass.Event{school}
	d.popupTick(at(14, 55))
	if got := d.popupUp(); got != nil {
		t.Errorf("%v popped up after a restart, tapped away elsewhere before it", got)
	}

	// A day on it is forgotten, and a flood of them keeps the newest.
	later := now.Add(popupGoneFor + time.Hour)
	for i := range config.PopupGoneMax + 5 {
		d.popupDismissedElsewhere(fmt.Sprintf("popup-%d", i), later.Add(time.Duration(i)*time.Second))
	}
	got := config.Get().Calendar.PopupGone
	if len(got) != config.PopupGoneMax {
		t.Fatalf("%d kept, want %d", len(got), config.PopupGoneMax)
	}
	for _, g := range got {
		if g.ID == popupMatch(school) || g.ID == "popup-0" {
			t.Errorf("%s still kept", g.ID)
		}
	}
	if last := got[len(got)-1]; last.ID != fmt.Sprintf("popup-%d", config.PopupGoneMax+4) {
		t.Errorf("the newest is %s", last.ID)
	}
}
