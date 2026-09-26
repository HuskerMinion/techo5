//go:build !dot && !spot

package display

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
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

// popupMorning is when all-day events pop up on day: as the night ends, or seven o'clock without one.
func popupMorning(day time.Time) time.Time {
	at := 7 * 60
	if _, to, ok := config.ParseWindow(config.Get().Screen.Night); ok {
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
		if len(c.PopupCalendars) > 0 && !slices.Contains(c.PopupCalendars, e.Calendar) {
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
	due := duePopups(events, now, c, popupShownNow())
	d.mu.Lock()
	for _, e := range due {
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
	night := inNight(config.Get().Screen.Night, now)
	d.mu.Unlock()

	// Saved after the lock is let go: writing the config waits on the flash, and every frame, touch and
	// turn waits on the lock.
	shown := popupShownNow()
	shown[popupKey(e)] = true
	keepPopupShown(shown, now)

	slog.Info("calendar: pop-up", "event", e.Summary, "starts", e.Start.Format(time.Kitchen), "night", night)
	component.Fire.Emit(component.Event{Name: CalendarEvent, Data: map[string]string{
		"event": "popup", "summary": e.Summary, "calendar": e.Calendar,
		"start": e.Start.Format(time.RFC3339), "device": config.Get().Device.Name,
	}})
	if !c.PopupSilent && !night && !config.Quiet() {
		go popupChime()
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

// popupFloor is the least the chime plays at, in volume steps, so it is heard.
const popupFloor = 6

// popupChime is the pop-up's sound: two soft falling notes, once.
func popupChime() {
	notes := []speaker.Note{{Freq: 988, Ms: 150}, {Freq: 0, Ms: 60}, {Freq: 784, Ms: 260}}
	claim := speaker.Sound().Claim("calendar", func(ctx context.Context, pl *speaker.Player) error {
		pl.Bell(max(pl.Step(), popupFloor), 0.5, notes...)
		select {
		case <-ctx.Done():
		case <-time.After(700 * time.Millisecond):
		}
		return nil
	})
	<-claim.Done()
}

// dismissPopup takes the pop-up down, and has the next in turn, if any, come up at once.
func (d *Display) dismissPopup() {
	d.mu.Lock()
	d.popup = nil
	d.popupNext = time.Time{}
	d.mu.Unlock()
}

// popupUp is the pop-up on the screen, if one is.
func (d *Display) popupUp() *hass.Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.popup
}
