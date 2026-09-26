//go:build !dot && !spot

package display

import (
	"image"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// popupBox is where an event's pop-up sits: the middle of the screen, a little smaller than a reminder.
func (r *renderer) popupBox() image.Rectangle {
	w, h := r.s(680), r.s(250)
	return image.Rect((r.w-w)/2, (r.h-h)/2, (r.w-w)/2+w, (r.h-h)/2+h)
}

// popupCard is an event coming up: its calendar and how soon, its title, and its time.
func (r *renderer) popupCard(s scene, e hass.Event) {
	box := r.popupBox()
	r.roundShadow(box, r.cardRad(), float64(r.s(34)), r.s(12), shadowAlpha()*1.3)
	r.roundFill(box, r.cardRad(), surface(4), surface(2))
	r.roundHighlight(box, r.cardRad())

	in := r.rowIn()
	width := box.Dx() - 2*in
	heading := strings.ToUpper(popupSoon(e, s.now))
	if name := popupCalendarName(e.Calendar); name != "" {
		heading += "  ·  " + strings.ToUpper(name)
	}
	hintW := r.width(r.tiny, dismissHint)
	r.rightText(r.tiny, dismissHint, box.Max.X-in, box.Min.Y+r.s(52), dim)
	r.text(r.tiny, clipText(r, r.tiny, heading, width-hintW-r.s(24)), box.Min.X+in, box.Min.Y+r.s(52), amber)
	r.text(r.title, clipText(r, r.title, e.Summary, width), box.Min.X+in, box.Min.Y+r.s(124), cream)
	r.text(r.small, clipText(r, r.small, eventWhen(e, s.now), width), box.Min.X+in, box.Min.Y+r.s(180), dim)
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
