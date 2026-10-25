//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"math"
	"slices"
	"strings"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// The Show's Binary, World, Agenda, Glow, Nixie, Stack and Overlap clock styles (clock_style.go), each
// in the box styledClock gives it, and the name a swipe that turned to a style shows for a moment.

// binaryStyle is the time in lights, a column per digit: hours, minutes and seconds, each digit's
// value counted up from the bottom light (1, 2, 4, 8). The time is written small under them, with the
// date, for anybody not counting.
func (r *renderer) binaryStyle(s scene, box image.Rectangle) {
	digits := binaryDigits(s.now)
	foot := r.s(100) // the time and the date under the lights
	room := box.Dy() - foot
	pitch := math.Min(float64(room)/4, float64(box.Dx())/7.6)
	rad := pitch * 0.36
	gap := pitch * 0.8 // between the hours, the minutes and the seconds
	x := float64(box.Min.X) + (float64(box.Dx())-(6*pitch+2*gap))/2 + pitch/2
	bottom := float64(box.Min.Y+room) - pitch/2
	ghost := lerp(walnut, amber, 0.13)
	for i, d := range digits {
		for bit := range binaryBits[i] {
			c := ghost
			if d&(1<<bit) != 0 {
				c = amber
			}
			r.fxDisc(x, bottom-float64(bit)*pitch, rad, c, 1)
		}
		x += pitch
		if i == 1 || i == 3 {
			x += gap
		}
	}
	tf := r.styleFace(true, 40)
	t := clockHM(s.now) + s.now.Format(":05")
	if suffix := clockSuffix(s.now); suffix != "" {
		t += " " + suffix
	}
	r.text(tf, t, box.Min.X+(box.Dx()-r.width(tf, t))/2, box.Max.Y-r.s(52), cream)
	r.styleDateUnder(s, box, box.Max.Y-r.s(8))
}

// worldStyle is the time here and in up to three other places, a row each: the place, how its day
// differs from here, and its time.
func (r *renderer) worldStyle(s scene, box image.Rectangle) {
	type row struct {
		name, day string
		at        time.Time
		here      bool
	}
	rows := []row{{name: locale.Here(screenLang()), at: s.now, here: true}}
	for _, p := range s.style.places {
		rows = append(rows, row{name: p.name, day: placeDay(s.now, p.loc), at: s.now.In(p.loc)})
	}
	h := box.Dy() / len(rows)
	hd := h * 100 / r.s(100) // the row's height, in the units faces are sized in
	size := min(84, hd*62/100)
	tf := r.styleFace(true, size)
	af := r.styleFace(true, max(16, size*2/3)) // AM and PM, in step with the time
	nf := r.styleFace(true, min(40, size/2+6))
	sf := r.styleFace(false, min(28, max(22, hd*30/100)))
	// The place's name over how its day differs, where the row has room for two lines; on one line
	// where it has not.
	gap := r.s(10)
	block := capHeight(nf) + gap + capHeight(sf)
	twoLines := block <= h-r.s(12)
	for i, w := range rows {
		top := box.Min.Y + i*h
		base := top + (h+capHeight(tf))/2
		if i > 0 {
			r.fxLine(float64(box.Min.X), float64(top), float64(box.Max.X), float64(top), float64(r.s(2)), ember, 1)
		}
		t, suffix := clockHM(w.at), clockSuffix(w.at)
		sw := 0
		if suffix != "" {
			sw = r.width(af, suffix) + r.s(10)
		}
		tx := box.Max.X - r.width(tf, t) - sw
		r.text(tf, t, tx, base, cream)
		if suffix != "" {
			r.text(af, suffix, box.Max.X-sw+r.s(10), base, dateColor(amber))
		}
		nc := cream
		if w.here {
			nc = amber
		}
		room := tx - box.Min.X - r.s(24)
		day := w.day
		if w.here {
			day = locale.Weekday(s.now, screenLang()) + ", " + locale.MonthDay(s.now, screenLang()) + alarmSuffix(s)
		}
		if w.here {
			// Here is today: its date opens the calendar, as the date does on the other styles.
			r.setDateAt(image.Rect(box.Min.X, top, box.Min.X+room, top+h))
		}
		name := r.clipTo(nf, w.name, room)
		switch {
		case day == "":
			r.text(nf, name, box.Min.X, top+(h+capHeight(nf))/2, nc)
		case twoLines:
			nb := top + (h-block)/2 + capHeight(nf)
			r.text(nf, name, box.Min.X, nb, nc)
			r.text(sf, r.clipTo(sf, day, room), box.Min.X, nb+gap+capHeight(sf), dim)
		default:
			nb := top + (h+capHeight(nf))/2
			r.text(nf, name, box.Min.X, nb, nc)
			x := box.Min.X + r.width(nf, name) + r.s(14)
			r.text(sf, r.clipTo(sf, day, box.Min.X+room-x), x, nb, dim)
		}
	}
}

// agendaStyle is the time on the left and the coming events down the right: the coming week's that
// have not ended, soonest first, each day under its heading, as many as fit.
func (r *renderer) agendaStyle(s scene, box image.Rectangle) {
	col := box.Min.X + box.Dx()*42/100
	hm := clockHM(s.now)
	tf := r.styleFace(true, 112)
	if tw := r.width(tf, hm); tw > col-box.Min.X-r.s(40) {
		tf = r.styleFace(true, 112*(col-box.Min.X-r.s(40))/tw)
	}
	base := box.Min.Y + r.s(20) + capHeight(tf)
	r.text(tf, hm, box.Min.X, base, cream)
	if suffix := clockSuffix(s.now); suffix != "" {
		// The fixed step under the digits, or more where the suffix needs it: a small gap, then the
		// suffix by its own height. The step alone let a taller font's AM or PM reach up into the time;
		// the screen's own font fits the step exactly, so with it nothing moves.
		step := max(r.s(48), r.s(6)+capHeight(r.ampm))
		r.text(r.ampm, suffix, box.Min.X, base+step, dateColor(amber))
		base += step
	}
	day := r.styleFace(true, 34)
	left := col - r.s(30) - box.Min.X
	r.text(day, r.clipTo(day, locale.Weekday(s.now, screenLang()), left), box.Min.X, base+r.s(64), cream)
	date := locale.MonthDay(s.now, screenLang())
	r.text(r.small, r.clipTo(r.small, date, left), box.Min.X, base+r.s(108), dateColor(dim))
	r.setDateAt(image.Rect(box.Min.X, base+r.s(30), col-r.s(30), base+r.s(118)))
	if a := alarmSuffix(s); a != "" {
		r.text(r.tiny, r.clipTo(r.tiny, strings.TrimPrefix(a, "  ·  "), left), box.Min.X, base+r.s(150), amber)
	}

	r.fxLine(float64(col-r.s(20)), float64(box.Min.Y), float64(col-r.s(20)), float64(box.Max.Y), float64(r.s(2)), ember, 1)
	head := r.styleFace(true, 20)
	y := box.Min.Y + r.s(28)
	lang := screenLang()
	r.text(head, strings.ToUpper(locale.Today(lang)), col, y, dim)
	y += r.s(48)
	if len(s.style.next) == 0 {
		r.text(r.small, locale.NothingOn(lang), col, y, dim)
		return
	}
	events := slices.Clone(s.style.next)
	slices.SortStableFunc(events, func(a, b hass.Event) int { return a.Start.Compare(b.Start) })
	today := time.Date(s.now.Year(), s.now.Month(), s.now.Day(), 0, 0, 0, 0, s.now.Location())
	tomorrow := today.AddDate(0, 0, 1)
	heads := agendaHeads(events, s.now, lang)
	tx := col + r.s(130)
	if !events[0].Start.Before(tomorrow) {
		// "Nothing else" only after a day that had something; a day with nothing at all says so.
		none := locale.NothingToday(lang)
		if s.style.hadToday {
			none = locale.NothingElse(lang)
		}
		r.text(r.small, none, col, y, dim)
		y += r.s(48)
	}
	for i, e := range events {
		if heads[i] != "" {
			if y+r.s(56) > box.Max.Y {
				return
			}
			y += r.s(10)
			r.text(head, r.clipTo(head, strings.ToUpper(heads[i]), box.Max.X-col), col, y, dim)
			y += r.s(46)
		}
		if y > box.Max.Y {
			return
		}
		when := locale.AllDay(lang)
		switch {
		case !e.AllDay && !e.Start.After(s.now):
			when = locale.Now(lang)
		case !e.AllDay:
			when = clockText(e.Start)
		}
		r.text(r.tiny, when, col, y, amber)
		r.text(r.small, r.clipTo(r.small, e.Summary, box.Max.X-tx), tx, y, cream)
		y += r.s(48)
	}
}

// agendaHeads is the heading the Agenda draws above each event, or nothing when it goes under the one
// before it: today's events go under Today, drawn already; tomorrow's under Tomorrow, and each day after
// that under its name and date. events are soonest first.
func agendaHeads(events []hass.Event, now time.Time, lang string) []string {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrow := today.AddDate(0, 0, 1)
	said := today
	heads := make([]string, len(events))
	for i, e := range events {
		st := e.Start.In(now.Location())
		on := time.Date(st.Year(), st.Month(), st.Day(), 0, 0, 0, 0, now.Location())
		if !on.After(said) {
			continue
		}
		heads[i] = locale.Tomorrow(lang)
		if !on.Equal(tomorrow) {
			heads[i] = locale.Weekday(on, lang) + ", " + locale.MonthDay(on, lang)
		}
		said = on
	}
	return heads
}

// glowStyle is the time over colors drifting slowly across the screen, like a lava lamp; glowGround
// has drawn the colors by then.
func (r *renderer) glowStyle(s scene, box image.Rectangle) {
	r.bigStyle(s, image.Rect(box.Min.X, box.Min.Y+r.s(24), box.Max.X, box.Max.Y-r.s(60)))
	r.styleDateUnder(s, box, box.Max.Y-r.s(8))
}

// nixieStyle is the time in Nixie tubes (nixie.go), the date under them. Where the box is wider than
// the tubes at its full height, they are shorter and the date comes up under them, the two kept
// together in the middle of the box; in a corner, at its foot.
func (r *renderer) nixieStyle(s scene, box image.Rectangle) {
	ampm, face := clockSuffix(s.now), r.ampm
	if r.cornered {
		face = r.styleFace(true, 36)
	}
	under := r.s(60) // the date's line
	h := r.nixieFit(box.Dx(), box.Dy()-r.s(8)-under, ampm, face)
	top := box.Min.Y + (box.Dy()-h-under)/2
	if r.cornered {
		top = box.Max.Y - h - under
	}
	r.nixieClock(&r.nixie, clockDigits(s.now), ampm, image.Rect(box.Min.X, top, box.Max.X, top+h), face, dateColor(amber), walnut)
	r.styleDateUnder(s, box, top+h+under-r.s(8))
}

// stackStyle is the hours over the minutes, two figures each (figures.go), as large as the box allows:
// the hours in the text color, the minutes in the accent, the AM/PM small beside the minutes and the
// date under. The block keeps to the middle of the box and the AM/PM hangs off its side; in a corner,
// the two at the box's foot.
func (r *renderer) stackStyle(s scene, box image.Rectangle) {
	d := stackDigits(s.now)
	ampm, af := clockSuffix(s.now), r.ampm
	if r.cornered {
		af = r.styleFace(true, 36)
	}
	aw := 0
	if ampm != "" {
		aw = r.s(14) + r.width(af, ampm)
	}
	under := r.s(60) // the date's line
	const ref = 200
	slot, gap, h := stackSlots(r.styleFace(true, ref))
	k := min(float64(box.Dx()-2*aw)*0.96/float64(2*slot+gap), float64(box.Dy()-under-r.s(8))/(float64(h)*(2+stackLead)))
	f := r.styleFace(true, int(ref*k))
	h = figureHeight(f)
	tall := 2*h + int(float64(h)*stackLead)
	top := box.Min.Y + (box.Dy()-tall-under)/2
	if r.cornered {
		top = box.Max.Y - tall - under
	}
	mid := box.Min.X + box.Dx()/2
	hours := r.stackLine(&r.figures, f, [2]int{d[0], d[1]}, mid, top+h, cream)
	minutes := r.stackLine(&r.figures, f, [2]int{d[2], d[3]}, mid, top+tall, dateColor(amber))
	if ampm != "" {
		// Level with the minutes' tops.
		r.text(af, ampm, minutes.Max.X+r.s(14), minutes.Min.Y+capHeight(af), dim)
	}
	// The whole time opens the calendar, as the date does under the classic face.
	r.setDateAt(hours.Union(minutes).Inset(-r.s(8)))
	r.styleDateUnder(s, box, top+tall+under-r.s(8))
}

// overlapStyle is the time's figures very large, each reaching over the one before it (figures.go), the
// accent and the text color in turn; the AM/PM small after them, level with their tops, and the date
// under. In a corner, at the box's foot.
func (r *renderer) overlapStyle(s scene, box image.Rectangle) {
	digits := clockDigits(s.now)
	ampm, af := clockSuffix(s.now), r.ampm
	if r.cornered {
		af = r.styleFace(true, 36)
	}
	aw := 0
	if ampm != "" {
		aw = r.s(14) + r.width(af, ampm)
	}
	under := r.s(60) // the date's line
	faceAt := func(size int) font.Face { return r.styleFace(true, size) }
	f := overlapFit(faceAt, 300, digits, (box.Dx()-aw)*96/100, box.Dy()-under-r.s(8))
	_, w, h := overlapRow(f, digits)
	top := box.Min.Y + (box.Dy()-h-under)/2
	if r.cornered {
		top = box.Max.Y - h - under
	}
	at := r.overlapClock(&r.figures, f, digits, box.Min.X+(box.Dx()-w-aw)/2, top+h, amber, cream, dim)
	if ampm != "" {
		r.text(af, ampm, at.Max.X+r.s(14), top+capHeight(af), dateColor(amber))
	}
	// The whole time opens the calendar, as the date does under the classic face.
	r.setDateAt(image.Rect(at.Min.X, at.Min.Y, at.Max.X+aw, at.Max.Y).Inset(-r.s(8)))
	r.styleDateUnder(s, box, top+h+under-r.s(8))
}

// glowGround is the Glow style's colors over the whole screen, drawn first so that everything else on
// the clock page (the timers, the weather, the strips) is drawn over them. Over a photo there are
// none: the photo is what is behind the clock then.
func (r *renderer) glowGround(s scene) {
	if s.slideshow != nil {
		return
	}
	field := r.glow.glowField(r.w/glowCell+1, r.h/glowCell+1, s.now, walnut, [3]color.RGBA{amber, ember, lerp(dim, amber, 0.3)})
	r.glow.stretch(r.dst, field, nil)
}

// styleNameTag is the name of the style a swipe turned to, in the middle of the clock for a moment.
func (r *renderer) styleNameTag(s scene) {
	name := s.style.named
	if name == "" {
		return
	}
	f := r.styleFace(true, 30)
	w := r.width(f, name)
	pad := r.s(22)
	hgt := capHeight(f) + 2*r.s(18)
	b := image.Rect((r.w-w)/2-pad, (r.h-hgt)/2, (r.w+w)/2+pad, (r.h+hgt)/2)
	r.roundRect(b, b.Dy()/2, shift(walnut, 34))
	r.text(f, name, (r.w-w)/2, b.Max.Y-r.s(18), cream)
}
