//go:build !dot && !spot

package display

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Event pop-ups (docs/calendar-and-night-plan.md): an event coming up, on the screen over whatever is
// there, off unless somebody turned them on. A timed event pops up its lead time before it starts and
// stays until tapped or until it has been going ten minutes; an all-day event pops up once, in the
// morning. At night and in quiet hours it makes no sound and does not light a dark screen: it is there
// when the screen is next woken.

// CalendarEvent is the name of the Home Assistant event a pop-up fires.
const CalendarEvent = "esphome.techo5_calendar"

const (
	popupAfter  = 10 * time.Minute // a timed event's pop-up stays this long after it starts
	popupAllDay = 3 * time.Hour    // an all-day event's, this long after it came up
	popupEvery  = 15 * time.Second // how often the calendar is looked at for one coming up
)

// popupKey names an event once, so it pops up once.
func popupKey(e hass.Event) string {
	return e.Calendar + "\x00" + e.Start.Format(time.RFC3339) + "\x00" + e.Summary
}

// popupMatch names an event the same way on every device in the house, so a pop-up tapped away on one
// comes down on the others: its start and its title, without the calendar, which a device showing the
// event merged from two calendars may name differently. Hashed, to fit the link between devices.
func popupMatch(e hass.Event) string {
	sum := sha256.Sum256([]byte(e.Start.UTC().Format(time.RFC3339) + "\x00" + e.Summary))
	return fmt.Sprintf("popup-%x", sum[:12])
}

// popupGoneFor is how long an event tapped away on another device is kept from popping up here: past
// the longest it could still be due, an hour's lead and the ten minutes after it starts, or an
// all-day one's morning.
const popupGoneFor = 24 * time.Hour

// popupMorning is when all-day events pop up on day: as the night ends, or seven o'clock without one.
func popupMorning(day time.Time) time.Time {
	at := 7 * 60
	if to, ok := config.NightEndOn(config.Get().Screen.Night, config.Get().Screen.NightWeekend, day); ok {
		at = to
	}
	return time.Date(day.Year(), day.Month(), day.Day(), at/60, at%60, 0, 0, time.Local)
}

// duePopups is the events that should be up now and have not been: those on the calendars that pop
// up, a timed one from its lead time before it starts until ten minutes after, an all-day one on its
// first day from the morning on. Earliest first.
func duePopups(events []hass.Event, now time.Time, c config.Calendar, shown map[string]bool) []hass.Event {
	lead := time.Duration(c.PopupLead()) * time.Minute
	var out []hass.Event
	for _, e := range events {
		if len(c.PopupCalendars) > 0 && !slices.ContainsFunc(c.PopupCalendars, func(cal string) bool { return home.OnCalendar(e, cal) }) {
			continue
		}
		if shown[popupKey(e)] {
			continue
		}
		if e.AllDay {
			if c.PopupAllDayNever || !sameDay(e.Start, now) || now.Before(popupMorning(now)) {
				continue
			}
		} else if now.Before(e.Start.Add(-lead)) || !now.Before(e.Start.Add(popupAfter)) {
			continue
		}
		out = append(out, e)
	}
	slices.SortStableFunc(out, func(a, b hass.Event) int { return a.Start.Compare(b.Start) })
	return out
}

// popupTick looks, now and then, for events to pop up, and takes one down that has been up long
// enough. Events due while another is up wait their turn; a timed event puts an all-day one away, since
// the all-day one has been seen. Called from every frame, so it keeps its own pace.
func (d *Display) popupTick(now time.Time) {
	d.mu.Lock()
	if now.Before(d.popupNext) {
		d.mu.Unlock()
		return
	}
	d.popupNext = now.Add(popupEvery)
	if d.popup != nil && now.After(d.popupUntil) {
		d.popup = nil
	}
	d.mu.Unlock()

	c := config.Get().Calendar
	if !c.Popups || len(c.Sources) == 0 {
		d.mu.Lock()
		d.popup, d.popupQueue = nil, nil
		d.mu.Unlock()
		return
	}
	h := home.Get()
	events, _ := h.MonthEvents(now)
	if next := firstOfMonth(now).AddDate(0, 1, 0); next.Sub(now) < 2*time.Hour {
		// An event just after midnight at the turn of the month is in the next month's list.
		more, _ := h.MonthEvents(next)
		events = append(events, more...)
	}

	// Due and not yet shown; an event already waiting its turn, or up now, is not queued twice. An event
	// counts as shown - and is saved as shown - only once it comes up, so a restart while it waits loses
	// nothing.
	due := duePopups(popupCandidates(events, c), now, c, popupShownNow())
	d.mu.Lock()
	d.loadPopupGone(c.PopupGone)
	maps.DeleteFunc(d.popupGone, func(_ string, at time.Time) bool { return now.Sub(at) > popupGoneFor })
	for _, e := range due {
		if _, gone := d.popupGone[popupMatch(e)]; gone {
			continue
		}
		k := popupKey(e)
		if (d.popup != nil && popupKey(*d.popup) == k) || slices.ContainsFunc(d.popupQueue, func(q hass.Event) bool { return popupKey(q) == k }) {
			continue
		}
		d.popupQueue = append(d.popupQueue, e)
	}
	if d.popup != nil && d.popup.AllDay && slices.ContainsFunc(d.popupQueue, func(e hass.Event) bool { return !e.AllDay }) {
		d.popup = nil
	}
	if d.popup != nil || len(d.popupQueue) == 0 {
		d.mu.Unlock()
		return
	}
	// The next in turn, all-day ones after timed ones; a timed one long over by now is dropped.
	slices.SortStableFunc(d.popupQueue, func(a, b hass.Event) int {
		if a.AllDay != b.AllDay {
			if a.AllDay {
				return 1
			}
			return -1
		}
		return a.Start.Compare(b.Start)
	})
	var e hass.Event
	for len(d.popupQueue) > 0 {
		e, d.popupQueue = d.popupQueue[0], d.popupQueue[1:]
		if _, gone := d.popupGone[popupMatch(e)]; gone {
			continue
		}
		if e.AllDay || now.Before(e.End) || now.Before(e.Start.Add(popupAfter)) {
			d.popup = &e
			break
		}
	}
	if d.popup == nil {
		d.mu.Unlock()
		return
	}
	// Up until ten minutes after it starts, or two minutes from now for one that waited its turn.
	d.popupUntil = e.Start.Add(popupAfter)
	if d.popupUntil.Before(now.Add(2 * time.Minute)) {
		d.popupUntil = now.Add(2 * time.Minute)
	}
	if e.AllDay {
		d.popupUntil = now.Add(popupAllDay)
	}
	night := nightNow(now)
	d.mu.Unlock()

	// Saved after the lock is let go: writing the config waits on the flash, and every frame, touch and
	// turn waits on the lock.
	shown := popupShownNow()
	shown[popupKey(e)] = true
	keepPopupShown(shown, now)

	slog.Info("calendar: pop-up", "summary", e.Summary, "starts", e.Start.Format(time.Kitchen), "night", night)
	component.Fire.Emit(component.Event{Name: CalendarEvent, Data: map[string]string{
		"event": "popup", "summary": e.Summary, "calendar": e.Calendar,
		"start": e.Start.Format(time.RFC3339), "device": config.Get().Device.Name,
	}})
	if !c.PopupSilent && !night && !config.Quiet() {
		go notify.Chime()
	}
}

// popupShownNow is the events already popped up, as kept: a restart does not pop them up again.
func popupShownNow() map[string]bool {
	shown := map[string]bool{}
	for _, k := range config.Get().Calendar.PopupShown {
		shown[k] = true
	}
	return shown
}

// keepPopupShown saves which events have popped up, forgetting those that started over two days ago.
func keepPopupShown(shown map[string]bool, now time.Time) {
	var keep []string
	for k := range shown {
		parts := strings.SplitN(k, "\x00", 3)
		if len(parts) == 3 {
			if at, err := time.Parse(time.RFC3339, parts[1]); err == nil && now.Sub(at) > 48*time.Hour {
				continue
			}
		}
		keep = append(keep, k)
	}
	slices.Sort(keep)
	if err := config.Set().Calendar().PopupShown(keep); err != nil {
		slog.Warn("calendar: keeping the pop-ups shown failed", "err", err)
	}
}

// dismissPopup takes the pop-up down, and has the next in turn, if any, come up at once.
func (d *Display) dismissPopup() {
	d.mu.Lock()
	d.popup = nil
	d.popupNext = time.Time{}
	d.mu.Unlock()
}

// sendPopupDismissed tells every other device in the house that the event's pop-up was tapped away
// here; replaced in tests. A house with no word has nobody to tell, and a Show on its own is not
// warned about it at every tap.
var sendPopupDismissed = func(id string) {
	if config.Get().Home.HouseWord == "" {
		return
	}
	announce.Get().SendTo(nil, announce.Message{Kind: announce.KindPopupDismissed, ID: id})
}

// tapPopupAway is a tap on the pop-up: it comes down here and on every other device showing it, or
// about to, since whoever tapped it has seen it. A device without the event, or with its pop-up
// already gone, has nothing to do. It is read and put away under one lock, so one that popupTick
// puts up in between is neither put away unseen nor named to the house in place of this one.
func (d *Display) tapPopupAway() {
	d.mu.Lock()
	e := d.popup
	d.popup = nil
	d.popupNext = time.Time{}
	d.mu.Unlock()
	if e == nil {
		return
	}
	id := popupMatch(*e)
	safe.Go("pop-up dismissed", func() { sendPopupDismissed(id) })
}

// popupDismissedElsewhere takes down an event's pop-up tapped away on another device: the one up, or
// one waiting its turn, and keeps it from popping up later, as one whose lead time here is shorter
// would. Nothing is sent on: the device it was tapped on told every one.
func (d *Display) popupDismissedElsewhere(id string, now time.Time) {
	kept := config.Get().Calendar.PopupGone // read before the lock, as popupTick does
	d.mu.Lock()
	d.loadPopupGone(kept)
	// Forgotten here as well as in popupTick, which does not get that far with pop-ups off.
	maps.DeleteFunc(d.popupGone, func(_ string, at time.Time) bool { return now.Sub(at) > popupGoneFor })
	d.popupGone[id] = now
	up := d.popup != nil && popupMatch(*d.popup) == id
	if up {
		d.popup = nil
		d.popupNext = time.Time{}
	}
	d.popupQueue = slices.DeleteFunc(d.popupQueue, func(e hass.Event) bool { return popupMatch(e) == id })
	d.mu.Unlock()
	// Saved too, after the lock is let go (writing waits on the flash): a restart between hearing of it
	// and the event coming due here would otherwise pop it up all the same.
	if err := config.Set().Calendar().PopupGoneAdd(id, now, popupGoneFor); err != nil {
		slog.Warn("calendar: keeping a pop-up tapped away elsewhere failed", "err", err)
	}
	if up {
		slog.Info("calendar: pop-up tapped away on another device")
		d.wake()
	}
}

// loadPopupGone fills the events tapped away elsewhere from those kept in the config, the first time
// they are wanted after a start; after that the ones in memory are the same, and newer. kept is read
// from the config before d.mu is taken, since writing the config waits on the flash. Called with d.mu
// held.
func (d *Display) loadPopupGone(kept []config.PopupGone) {
	if d.popupGone != nil {
		return
	}
	d.popupGone = make(map[string]time.Time, len(kept))
	for _, g := range kept {
		d.popupGone[g.ID] = g.At
	}
}

// popupUp is the pop-up on the screen, if one is.
func (d *Display) popupUp() *hass.Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.popup
}

// popupsWaiting is how many events are due and waiting for the pop-up that is up to go.
func (d *Display) popupsWaiting() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.popupQueue)
}

// popupCandidates is the events that may pop up: not those of a calendar hidden on this screen's
// calendar page, which are hidden here everywhere, pop-ups as well.
func popupCandidates(events []hass.Event, c config.Calendar) []hass.Event {
	return visibleEvents(events, hiddenHere(c))
}
