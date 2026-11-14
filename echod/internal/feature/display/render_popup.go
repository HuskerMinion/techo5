//go:build !dot && !spot

package display

import (
	"image"
	"strconv"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// popupBox is where an event's pop-up sits: the reminder's box, so that the three cards are one card
// in one place and the one under another is covered whole.
func (r *renderer) popupBox() image.Rectangle { return r.reminderBox() }

// popupLines is as many lines as the card gives an event's title, over the line that says when.
const popupLines = 2

// popupCard is an event coming up: its calendar and how soon, its title, and its time. A title that
// fits one line sits lower with its time, so the pair is in the middle of the card under the heading
// instead of leaving the foot of it empty.
func (r *renderer) popupCard(s scene, e hass.Event) {
	box := r.popupBox()
	r.setPopupAt(box)
	heading := popupSoon(e, s.now)
	if name := popupCalendarName(e.Calendar); name != "" {
		heading += "  ·  " + name
	}
	in := r.rowIn()
	top, when := 158, 224
	if len(r.wrap(r.title, e.Summary, box.Dx()-2*in)) > 1 {
		top, when = 126, 126+cardLineH+62
	}
	r.noticeCard(box, 56, top, "", heading, e.Summary, popupLines)
	r.text(r.small, clipText(r, r.small, eventWhen(e, s.now), box.Dx()-2*in), box.Min.X+in, box.Min.Y+r.s(when), dim)
}

func (r *renderer) setPopupAt(b image.Rectangle) {
	r.weatherMu.Lock()
	r.popupAt = b
	r.weatherMu.Unlock()
}

// popupTapped is whether a tap at p landed on the pop-up as last drawn: none drawn, none tapped.
func (r *renderer) popupTapped(p image.Point) bool {
	r.weatherMu.Lock()
	defer r.weatherMu.Unlock()
	return !r.popupAt.Empty() && p.In(r.popupAt)
}

// popupSoon is how soon an event is, as its pop-up heads it: "In 15 minutes", "Starting now", "Today".
func popupSoon(e hass.Event, now time.Time) string {
	if e.AllDay {
		return "Today"
	}
	switch m := int(e.Start.Sub(now).Round(time.Minute) / time.Minute); {
	case m <= 0 && now.Before(e.End):
		return "Now"
	case m <= 0:
		return "Started"
	case m == 1:
		return "In a minute"
	case m < 60:
		return "In " + strconv.Itoa(m) + " minutes"
	case m == 60:
		return "In an hour"
	default:
		return "At " + clockText(e.Start)
	}
}

// popupCalendarName is a calendar's name as Home Assistant gives it.
func popupCalendarName(id string) string {
	for _, c := range home.Get().Calendars() {
		if c.ID == id {
			return c.Name
		}
	}
	return ""
}
