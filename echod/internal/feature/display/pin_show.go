//go:build !dot && !spot

package display

import (
	"image"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The PIN pad on the Show: what it is for and the dots on the left, the keys on the right, Cancel under
// the words.

// pinKeyRects are the twelve keys, in pinKeys' order.
func (r *renderer) pinKeyRects() []image.Rectangle {
	w, h, gap := r.s(96), r.s(70), r.s(12)
	x0 := r.w - r.margin - 3*w - 2*gap
	y0 := r.s(78) + r.tallerBy()/2 // in the middle of a panel taller than the Show 5's shape
	out := make([]image.Rectangle, 0, len(pinKeys))
	for i := range pinKeys {
		col, row := i%3, i/3
		x, y := x0+col*(w+gap), y0+row*(h+gap)
		out = append(out, image.Rect(x, y, x+w, y+h))
	}
	return out
}

func (r *renderer) pinCancel() image.Rectangle {
	w, h := r.s(150), r.s(56)
	y := r.h - r.s(40) - h
	return image.Rect(r.margin, y, r.margin+w, y+h)
}

func (r *renderer) pinPage(p pinView) {
	heading := tr("settings")
	if p.what != "" {
		heading = p.what
	}
	// All the room left of the keys, and a smaller face for a name too long for the title's (#324).
	room, face := r.pinKeyRects()[0].Min.X-r.margin-r.s(24), r.title
	if r.width(face, heading) > room {
		face = r.body
	}
	r.text(face, r.fit(face, heading, room), r.margin, r.s(130), cream)
	r.text(r.body, r.fit(r.body, p.title, room), r.margin, r.s(185), dim)
	// The dots: one for each digit typed, rings for the rest of a four-digit PIN.
	cx, cy, rad, gap := r.margin+r.s(16), r.s(245), r.s(12), r.s(40)
	for i := range max(p.digits, 4) {
		x := cx + i*gap
		if i < p.digits {
			r.disc(x, cy, rad, amber)
		} else {
			r.disc(x, cy, rad, dim)
			r.disc(x, cy, rad-r.s(3), walnut)
		}
	}
	if p.msg != "" {
		r.text(r.body, r.fit(r.body, p.msg, room), r.margin, r.s(310), amber)
	}
	c := r.pinCancel()
	// The program's button. The keys are the page's own input grid and keep their bevel.
	r.labelButton(c, tr("cancel"), btnSecondary)

	for i, k := range r.pinKeyRects() {
		r.bevel(k, shift(ember, 16), true)
		label := pinKeys[i]
		face := r.title
		switch label {
		case "back":
			label, face = tr("back"), r.small
		case "ok":
			label, face = tr("ok"), r.body
		}
		lw := r.width(face, label)
		y := k.Min.Y + k.Dy()/2 + r.s(14)
		if face == r.small {
			y = k.Min.Y + k.Dy()/2 + r.s(9)
		}
		r.text(face, label, k.Min.X+(k.Dx()-lw)/2, y, cream)
	}
}

// pinGesture takes a tap while the pad is up, and reports whether it did.
func (d *Display) pinGesture(g touch.Gesture) bool {
	if !pinIsOpen() {
		return false
	}
	if g.Kind != touch.Tap || d.r == nil {
		return true // the pad takes everything while it is up; only taps do anything
	}
	pt := image.Pt(g.X, g.Y)
	if pt.In(d.r.pinCancel()) {
		pinPress("cancel")
	}
	for i, k := range d.r.pinKeyRects() {
		if pt.In(k.Inset(-d.r.s(4))) {
			pinPress(pinKeys[i])
			break
		}
	}
	d.wake()
	return true
}
