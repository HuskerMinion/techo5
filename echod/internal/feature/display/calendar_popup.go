//go:build !dot && !spot

package display

import (
	"context"
	"log/slog"
	"slices"
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

// popupTick looks, now and then, for an event to pop up, and takes one down that has been up long
// enough. Called from every frame, so it keeps its own pace.
func (d *Display) popupTick(now time.Time) {
	d.mu.Lock()
	if now.Before(d.popupNext) {
		d.mu.Unlock()
		return
	}
	d.popupNext = now.Add(popupEvery)
	up := d.popup
	if up != nil && now.After(d.popupUntil) {
		d.popup, up = nil, nil
	}
	d.mu.Unlock()

	c := config.Get().Calendar
	if !c.Popups || up != nil || len(c.Sources) == 0 {
		return
	}
	h := home.Get()
	events, _ := h.MonthEvents(now)
	if next := firstOfMonth(now).AddDate(0, 1, 0); next.Sub(now) < 2*time.Hour {
		// An event just after midnight at the turn of the month is in the next month's list.
		more, _ := h.MonthEvents(next)
		events = append(events, more...)
	}
	d.mu.Lock()
	if d.popupShown == nil {
		d.popupShown = map[string]bool{}
	}
	due := duePopups(events, now, c, d.popupShown)
	if len(due) == 0 {
		d.mu.Unlock()
		return
	}
	e := due[0]
	d.popupShown[popupKey(e)] = true
	d.popup = &e
	d.popupUntil = e.Start.Add(popupAfter)
	if e.AllDay {
		d.popupUntil = now.Add(popupAllDay)
	}
	night := inNight(config.Get().Screen.Night, now)
	d.mu.Unlock()

	slog.Info("calendar: pop-up", "event", e.Summary, "starts", e.Start.Format(time.Kitchen), "night", night)
	component.Fire.Emit(component.Event{Name: CalendarEvent, Data: map[string]string{
		"event": "popup", "summary": e.Summary, "calendar": e.Calendar,
		"start": e.Start.Format(time.RFC3339), "device": config.Get().Device.Name,
	}})
	if !c.PopupSilent && !night && !config.Quiet() {
		go popupChime()
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

// dismissPopup takes the pop-up down.
func (d *Display) dismissPopup() {
	d.mu.Lock()
	d.popup = nil
	d.mu.Unlock()
}

// popupUp is the pop-up on the screen, if one is.
func (d *Display) popupUp() *hass.Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.popup
}
