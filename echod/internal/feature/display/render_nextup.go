//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The clock's next-up card: while an event is on, or one comes up today or within the next twelve
// hours, the foot of the clock carries it, its time and how long until it, and under it the days to a
// long event coming up ("Vacation in 11 days") and what comes after it. A tap opens the calendar on its
// day. Only where the glance strip would have nothing to show: a strip with chips of its own (a door
// open, the temperature) stays as it is, and there is no card. Not with the Dashboard and Agenda
// styles, which list the coming events already, nor with the clock at a bottom position (Clock
// position), where the card would sit on it.

// nextUpKept is the card worked out for a minute, the events as they were, and the settings that
// change it: a frame drawn in the same minute with nothing changed takes it as it is, rather than
// reading two months' events and choosing again, which the clock would do every frame.
type nextUpKept struct {
	key  string
	kept *nextUpCard
}

// card is nextUpFor, worked out again only when its key has changed.
func (k *nextUpKept) card(style string, now time.Time) *nextUpCard {
	c := config.Get().Calendar
	key := fmt.Sprint(style, now.Unix()/60, home.Get().CalendarVersion(), c.NoNextUp, c.NoCountdowns, c.Sources, c.Hidden)
	if key != k.key {
		k.key, k.kept = key, nextUpFor(style, now)
	}
	return k.kept
}

// nextUpFor is the card for now in the clock style shown, or nil for none.
func nextUpFor(style string, now time.Time) *nextUpCard {
	c := config.Get().Calendar
	if c.NoNextUp || len(c.Sources) == 0 || style == styleDashboard || style == styleAgenda {
		return nil
	}
	events, _ := agendaEvents(now)
	card, ok := pickNextUp(visibleEvents(events, hiddenHere(c)), now)
	if !ok {
		return nil
	}
	if c.NoCountdowns {
		card.big = nil
	}
	card.color = calendarColor(c.Sources, card.ev.Calendar)
	if card.big != nil {
		card.bigColor = calendarColor(c.Sources, card.big.Calendar)
	}
	return &card
}

// footerBusy is whether the footer has words in it, which the card keeps clear of.
func footerBusy(s scene) bool {
	return s.setupAsking || s.missed != "" || s.slideshowTrouble != "" || playingWord(s) != "" || s.bt.Connected != ""
}

// nextUpRect is where the card goes: along the foot of the clock, a line or two tall, clear of the
// Call button and of the footer's words.
func (r *renderer) nextUpRect(s scene) (box image.Rectangle, twoLines bool) {
	c := s.nextUp
	twoLines = c != nil && (c.big != nil || c.then != nil) && !footerBusy(s)
	h, bottom := r.s(66), r.h-r.s(46)
	if twoLines {
		h, bottom = r.s(104), r.h-r.s(20)
	} else if !footerBusy(s) {
		bottom = r.h - r.s(24)
	}
	left := r.margin
	if s.callButton {
		left = r.callButtonRect().Max.X + r.s(12)
	}
	return image.Rect(left, bottom-h, r.w-r.margin, bottom), twoLines
}

// drawNextUp draws the card.
//
// Over a slideshow photo the card is drawn as on the plain ground, outside the photo's care for its
// words (readable.go): it is a box of its own, opaque, so the words on it read without help. Through
// that care they did not: the first pass laid a dark patch under each of its lines, which spilled past
// the card's rounded corners onto the photo, and the second turned every small word photoGold, so the
// filled countdown chip read gold on amber and "Next:", the title and the time lost their colors.
func (r *renderer) drawNextUp(s scene) {
	c := s.nextUp
	if c == nil || r.over.record {
		return
	}
	if photo := r.over.photo; photo != nil {
		r.over.photo = nil
		defer func() { r.over.photo = photo }()
	}
	box, two := r.nextUpRect(s)
	r.roundRect(box, r.s(18), calWash(16))
	col := c.color
	if s.nightDim {
		// The night light: no filled accent or bright calendar color to light the room.
		col = calFade(col, 0.4)
	}
	stripeTop, base := box.Min.Y+r.s(15), box.Min.Y+r.s(44)
	if two {
		stripeTop, base = box.Min.Y+r.s(18), box.Min.Y+r.s(47)
	}
	r.roundRect(image.Rect(box.Min.X+r.s(18), stripeTop, box.Min.X+r.s(24), stripeTop+r.s(36)), r.s(3), col)

	right := box.Max.X - r.s(20)
	if !config.Get().Calendar.NoCountdowns {
		if t := countdownText(s.now, c.ev.Start, c.ev.End); t != "" {
			right = r.countChip(t, right, base-r.s(11), !s.nightDim, r.tiny) - r.s(16)
		}
	}
	x := box.Min.X + r.s(40)
	r.text(r.small, "Next:", x, base, amber)
	x += r.width(r.small, "Next:") + r.s(12)
	when := "  ·  " + nextWhen(c.ev, s.now)
	title := clipText(r, r.small, c.ev.Summary, right-x-r.width(r.small, when))
	r.text(r.small, title, x, base, cream)
	x += r.width(r.small, title)
	if x+r.width(r.small, when) <= right {
		r.text(r.small, when, x, base, dim)
	}

	if two {
		y2 := box.Min.Y + r.s(86)
		half := box.Min.X + box.Dx()/2
		thenX := box.Max.X - r.s(20)
		if c.then != nil {
			t := "Then: " + c.then.Summary + " · " + nextWhen(*c.then, s.now)
			room := thenX - half
			if c.big == nil {
				room = thenX - box.Min.X - r.s(40)
			}
			t = clipText(r, r.tiny, t, room)
			thenX -= r.width(r.tiny, t)
			r.text(r.tiny, t, thenX, y2, dim)
		}
		if c.big != nil {
			icon := "calendar-range"
			if k := keywordIcon(c.big.Summary); k != "" && !config.Get().Calendar.NoIcons {
				icon = k
			}
			r.mdiAt(icon, box.Min.X+r.s(52), y2-r.s(9), 24, c.bigColor)
			t := clipText(r, r.tiny, daysCountdown(c.big.Summary, c.days), thenX-r.s(24)-(box.Min.X+r.s(72)))
			r.text(r.tiny, t, box.Min.X+r.s(72), y2, dim)
		}
	}
	day := time.Date(c.ev.Start.Year(), c.ev.Start.Month(), c.ev.Start.Day(), 0, 0, 0, 0, time.Local)
	if c.ev.Start.Before(s.now) {
		day = time.Date(s.now.Year(), s.now.Month(), s.now.Day(), 0, 0, 0, 0, time.Local) // on since before today
	}
	r.setNextCard(box, day)
}

func (r *renderer) setNextCard(b image.Rectangle, day time.Time) {
	r.weatherMu.Lock()
	r.nextCardAt, r.nextCardDay = b, day
	r.weatherMu.Unlock()
}

// nextCardTapped is whether a tap at p landed on the next-up card, as last drawn, and its event's day.
func (r *renderer) nextCardTapped(p image.Point) (time.Time, bool) {
	r.weatherMu.Lock()
	defer r.weatherMu.Unlock()
	return r.nextCardDay, !r.nextCardAt.Empty() && p.In(r.nextCardAt)
}
