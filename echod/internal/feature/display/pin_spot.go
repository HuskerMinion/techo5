//go:build spot

package display

import (
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The PIN pad on the Spot: round keys in the middle of the face, the dots above them, and Cancel at the
// top where a thumb reaches it.

const (
	pinKeyR    = 30
	pinKeyGapX = 74
	pinRowTop  = 212
	pinRowGap  = 60
	pinCancelY = 70
)

func pinKeyCenter(i int) (x, y float64) {
	return center + float64((i%3-1)*pinKeyGapX), float64(pinRowTop + (i/3)*pinRowGap)
}

func (r *roundRenderer) pinFace(p pinView) {
	r.centered(r.small, "Cancel", pinCancelY, colAccent)
	r.centered(r.small, p.title, 112, colText)
	n := max(p.digits, 4)
	gap := 30
	x0 := center - (n-1)*gap/2
	for i := range n {
		x := float64(x0 + i*gap)
		if i < p.digits {
			r.discAt(x, 132, 9, colText)
		} else {
			r.discAt(x, 132, 9, colDim)
			r.discAt(x, 132, 6, colBackground)
		}
	}
	if p.msg != "" {
		r.centered(r.small, p.msg, 168, colTimer)
	}
	for i, k := range pinKeys {
		x, y := pinKeyCenter(i)
		r.discAt(x, y, pinKeyR, colTrack)
		label, face := k, r.title
		switch k {
		case "back":
			label, face = "Back", r.small
		case "ok":
			label, face = "OK", r.body
		}
		lift := 11
		if face == r.body {
			lift = 8
		} else if face == r.small {
			lift = 6
		}
		r.text(face, label, int(x)-r.width(face, label)/2, int(y)+lift, colText)
	}
}

// pinGesture takes a tap while the pad is up, and reports whether it did.
func (d *Display) pinGesture(g touch.Gesture) bool {
	if !pinIsOpen() {
		return false
	}
	if g.Kind != touch.Tap {
		return true
	}
	if g.Y < pinCancelY+20 {
		pinPress("cancel")
		d.wake()
		return true
	}
	for i, k := range pinKeys {
		x, y := pinKeyCenter(i)
		if math.Hypot(float64(g.X)-x, float64(g.Y)-y) <= pinKeyR+6 {
			pinPress(k)
			break
		}
	}
	d.wake()
	return true
}
