//go:build spot

package display

import (
	"image"
	"image/color"
	"math"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// The Spot's Binary, World, Agenda and Glow clock styles (clock_style.go), laid out for the circle,
// and the name a swipe that turned to a style shows for a moment.

// binaryFace is the time in lights, a column per digit, each counted up from the bottom light (1, 2,
// 4, 8); the time written small under them, and the day.
func (r *roundRenderer) binaryFace(s roundScene) {
	digits := binaryDigits(s.now)
	const pitch, rad, gap = 52.0, 18.0, 26.0
	x := float64(center) - (6*pitch+2*gap)/2 + pitch/2
	const bottom = 300.0
	ghost := lerp(colBackground, colAccent, 0.16)
	for i, d := range digits {
		for bit := range binaryBits[i] {
			c := ghost
			if d&(1<<bit) != 0 {
				c = colAccent
			}
			r.discAt(x, bottom-float64(bit)*pitch, rad, c)
		}
		x += pitch
		if i == 1 || i == 3 {
			x += gap
		}
	}
	t := clockHM(s.now) + s.now.Format(":05")
	if suffix := clockSuffix(s.now); suffix != "" {
		t += " " + suffix
	}
	r.centered(r.styleFace(true, 30), t, 364, colText)
	r.footLine(s, 404, shortDay(s))
}

// spotWorld is how many of the World style's places the round face has room for.
const spotWorld = 2

// worldFace is the time here and in up to two other places, a row each.
func (r *roundRenderer) worldFace(s roundScene) {
	type row struct {
		name, day string
		at        time.Time
		here      bool
	}
	rows := []row{{name: locale.Here(screenLang()), day: locale.DayAndNumber(s.now, screenLang()), at: s.now, here: true}}
	for _, p := range s.style.places[:min(len(s.style.places), spotWorld)] {
		rows = append(rows, row{name: p.name, day: placeDay(s.now, p.loc), at: s.now.In(p.loc)})
	}
	tf := r.styleFace(true, 56)
	nf := r.styleFace(true, 24)
	df := r.styleFace(false, 18)
	top, h := 104, 272/len(rows)
	for i, w := range rows {
		mid := top + i*h + h/2
		if i > 0 {
			y := float64(top + i*h)
			half := float64(chord(top+i*h)/2 - 70)
			r.line(center-half, y, center+half, y, 2, colTrack)
		}
		half := chord(mid)/2 - 44
		left, right := center-half, center+half
		t, suffix := clockHM(w.at), clockSuffix(w.at)
		sw := 0
		if suffix != "" {
			sw = r.width(r.label, suffix) + 6
		}
		tx := right - r.width(tf, t) - sw
		base := mid + capHeightOf(tf)/2
		r.text(tf, t, tx, base, colText)
		if suffix != "" {
			r.text(r.label, suffix, right-sw+6, base, colAccent)
		}
		nc := colText
		if w.here {
			nc = colAccent
		}
		room := tx - left - 16
		if w.day == "" {
			r.text(nf, spotClip(r, nf, w.name, room), left, mid+capHeightOf(nf)/2, nc)
			continue
		}
		r.text(nf, spotClip(r, nf, w.name, room), left, mid-4, nc)
		r.text(df, spotClip(r, df, w.day, room), left, mid+22, colDim)
	}
	r.footLine(s, 412, "")
}

// agendaFace is the time at the top and the coming events under it, as many as fit.
func (r *roundRenderer) agendaFace(s roundScene) {
	h := r.styleFace(true, 64)
	hm := clockHM(s.now)
	ampm := clockSuffix(s.now)
	aw := 0
	if ampm != "" {
		aw = r.width(r.label, ampm) + 6
	}
	x := center - (r.width(h, hm)+aw)/2
	r.text(h, hm, x, 134, colText)
	if ampm != "" {
		r.text(r.label, ampm, x+r.width(h, hm)+6, 134, colAccent)
	}
	r.centered(r.styleFace(false, 20), locale.LongDate(s.now, screenLang()), 166, colDim)
	r.line(90, 188, 390, 188, 2, colTrack)
	f := r.styleFace(false, 20)
	if len(s.style.next) == 0 {
		r.centered(f, locale.NothingOn(screenLang()), 240, colDim)
		r.footLine(s, 300, "")
		return
	}
	n := 4
	if len(s.timers) > 0 || s.missed != "" {
		n = 3
		r.footLine(s, 386, "")
	}
	for i, e := range s.style.next[:min(len(s.style.next), n)] {
		y := 226 + i*40
		half := chord(y-8)/2 - 40
		left := center - half
		when := dashWhenSpot(e.Start, e.AllDay, s.now)
		r.text(f, when, left, y, colTimer)
		tx := left + 96
		r.text(f, spotClip(r, f, e.Summary, center+half-tx), tx, y, colText)
	}
}

// glowFace is the time over colors drifting slowly across the face, like a lava lamp. Over a photo the
// colors are left out: the photo is what is behind the clock then.
func (r *roundRenderer) glowFace(s roundScene) {
	if s.slideshow == nil {
		field := r.glow.glowField(side/glowCell+1, side/glowCell+1, s.now, colBackground,
			[3]color.RGBA{colAccent, colTimer, lerp(colDim, colAccent, 0.4)})
		// Inside the status ring only: the ring still says what it says.
		r.glow.stretch(r.dst, field, glowMask())
	}
	r.statusWord(s, 140)
	r.timeLine(s.now, 262)
	r.centered(r.small, locale.LongDate(s.now, screenLang()), 312, colText)
	r.footLine(s, 360, "")
}

// glowMask is the face inside the status ring, its edge smoothed, made once.
var glowMask = sync.OnceValue(func() *image.Alpha {
	m := image.NewAlpha(image.Rect(0, 0, side, side))
	edge := float64(rimIn) - 3
	for y := range side {
		for x := range side {
			d := math.Hypot(float64(x)+0.5-center, float64(y)+0.5-center)
			m.Pix[m.PixOffset(x, y)] = uint8(255 * math.Min(math.Max(edge-d, 0), 1))
		}
	}
	return m
})

// styleNameTag is the name of the style a swipe turned to, near the top of the face for a moment.
func (r *roundRenderer) styleNameTag(s roundScene) {
	name := s.style.named
	if name == "" {
		return
	}
	f := r.styleFace(true, 26)
	w := r.width(f, name)
	// Near the top, where the faces keep clear of; the Agenda's time is there, so under its list.
	top := 62
	if s.style.style() == styleAgenda {
		top = 396
	}
	b := image.Rect(center-w/2-18, top, center+w/2+18, top+capHeightOf(f)+26)
	fill := color.RGBA{36, 42, 52, 255}
	r.roundFill(b, float64(b.Dy())/2, fill, fill)
	r.text(f, name, center-w/2, b.Max.Y-13, colText)
}
