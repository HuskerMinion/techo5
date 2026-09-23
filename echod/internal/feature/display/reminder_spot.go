//go:build spot

package display

import (
	"image"

	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// A reminder on the round face. It takes the whole face, as an announcement does, since a circle has
// no corner to put a card in. Its words can run long, and a face that stopped after two lines would
// drop the rest without a sign: so they scroll inside a box, with the settings screen's own fade and
// rim thumb saying there is more, and a drag moves them.

// reminderLineH is one line of a reminder's words.
const reminderLineH = 46

// reminderFace draws a reminder with its words scrolled up by scroll pixels, and records how far they
// can scroll, for the drag.
func (r *roundRenderer) reminderFace(s roundScene) {
	r.centred(r.label, "REMINDER", 150, colAccent)
	top := 210
	if s.reminderFrom != "" {
		r.centred(r.small, clip(r.small, r, s.reminderFrom, 300), 196, colDim)
		top = 220
	}
	// A little over two lines, so a third that fades out at the foot says there is more.
	box := image.Rect(70, top, side-70, top+104)
	lines := r.wrap(r.title, s.reminder.Label, box.Dx())
	maxScroll := max(len(lines)*reminderLineH-box.Dy(), 0)
	scroll := min(max(s.reminderScroll, 0), maxScroll)

	// Lines are drawn whole, then the frame from before them is put back above and below the box,
	// the way the settings card clips its rows, so neither the room nor the hint is drawn over.
	under := append([]uint8(nil), r.dst.Pix...)
	for i, line := range lines {
		y := box.Min.Y + i*reminderLineH - scroll
		if y+reminderLineH <= box.Min.Y || y >= box.Max.Y {
			continue
		}
		r.centred(r.title, line, y+32, colText)
	}
	hint := "tap to dismiss"
	if maxScroll > 0 {
		r.restore(under, image.Rect(0, 0, side, box.Min.Y))
		r.restore(under, image.Rect(0, box.Max.Y, side, side))
		r.scrollHints(box, scroll, maxScroll, colBackground)
		hint = "tap to dismiss · drag for more"
	}
	r.centred(r.small, hint, box.Max.Y+30, colDim)

	r.zmu.Lock()
	r.reminderMax = maxScroll
	r.zmu.Unlock()
}

// reminderScrollLimit is how far the reminder on the face could scroll in the frame last drawn.
func (r *roundRenderer) reminderScrollLimit() int {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return r.reminderMax
}

// reminderGesture is a finger on a reminder's face: a tap puts it away, here and wherever else it
// went off; a drag scrolls its words. Every gesture is its, since the face is.
func (d *Display) reminderGesture(g touch.Gesture) {
	switch g.Kind {
	case touch.Tap:
		go remind.Get().Stop()
	case touch.Hold:
		d.mu.Lock()
		d.reminderDragging, d.reminderDragFrom, d.reminderDragScroll = true, g.Y, d.reminderScroll
		d.mu.Unlock()
	case touch.Drag:
		limit := 0
		if d.r != nil {
			limit = d.r.reminderScrollLimit()
		}
		d.mu.Lock()
		if d.reminderDragging {
			d.reminderScroll = min(max(d.reminderDragScroll+d.reminderDragFrom-g.Y, 0), limit)
		}
		d.mu.Unlock()
	case touch.Release:
		d.mu.Lock()
		d.reminderDragging = false
		d.mu.Unlock()
	}
	d.wake()
}

// reminderLights brings the panel up for a reminder going off and has the finger followed, so its
// words can be dragged; and puts following back the way the rest of the face wants it when it goes.
func (d *Display) reminderLights() {
	r, showing := remind.Get().Showing()
	d.mu.Lock()
	on, sheet := d.on, d.sheetOpen
	if showing {
		if d.menuOpen {
			d.closeMenu()
		}
		if r.ID != d.reminderID {
			d.reminderID, d.reminderScroll, d.reminderDragging = r.ID, 0, false
		}
	}
	d.mu.Unlock()
	switch {
	case showing:
		d.followFingers(true)
		if !on {
			d.apply(true, d.ceilingOrDefault(), false)
		}
	case !sheet:
		d.followFingers(false)
	}
	d.wake()
}
