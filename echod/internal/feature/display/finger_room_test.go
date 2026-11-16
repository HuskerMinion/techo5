//go:build !dot && !spot

package display

import (
	"image"
	"testing"
)

// The strip's X is taken by a finger in its corner of the strip, not only on the mark, and never by
// one on the button beside it.
func TestTheStripsCloseHasAFingersRoom(t *testing.T) {
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		_, _, next, x := r.stripButtons()
		hit := r.stripCloseHit()
		if !x.In(hit) {
			t.Errorf("%s: the X %v is outside where it is tapped %v", panel.name, x, hit)
		}
		if hit.Dx() < r.s(56) || hit.Dy() < r.s(50) {
			t.Errorf("%s: the X is tapped in %v, no more than the mark", panel.name, hit)
		}
		if !hit.In(r.stripRect()) {
			t.Errorf("%s: %v reaches off the strip %v", panel.name, hit, r.stripRect())
		}
		for name, b := range map[string]image.Rectangle{"forward": next, "the library": r.stripLibrary()} {
			if hit.Overlaps(b) {
				t.Errorf("%s: the X's %v reaches %s at %v", panel.name, hit, name, b)
			}
		}
	}
}

// Each of the now-playing page's buttons is taken a little way round it, and no two reach each other.
func TestNowPlayingButtonsHaveAFingersRoom(t *testing.T) {
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		for _, modern := range []bool{false, true} {
			back, play, next := r.musicButtons(modern)
			all := map[string]image.Rectangle{
				"done": r.doneButton(), "star": r.favButton(), "library": r.libraryButton("Library", modern),
				"back": back, "play": play, "next": next,
			}
			for a, ra := range all {
				if got := r.buttonReach(ra); !ra.In(got) || got.Dy() < ra.Dy()+r.s(20) {
					t.Errorf("%s modern %v: %s is tapped in %v, drawn in %v", panel.name, modern, a, got, ra)
				}
				for b, rb := range all {
					if a < b && !ra.Overlaps(rb) && r.buttonReach(ra).Overlaps(r.buttonReach(rb)) {
						t.Errorf("%s modern %v: %s and %s reach each other", panel.name, modern, a, b)
					}
				}
			}
		}
	}
}

// A finger just off a sheet's part is on the nearest one within the slop, and on none beyond it.
func TestASheetsPartsHaveSlop(t *testing.T) {
	card := image.Rect(0, 0, 400, 200)
	zones := []mediaZone{
		{r: image.Rect(20, 20, 120, 64), kind: mediaPartVolume, index: 1},
		{r: image.Rect(130, 20, 230, 64), kind: mediaPartVolume, index: 2},
	}
	for _, c := range []struct {
		x, y, slop, want int
	}{
		{122, 40, 5, 1}, {127, 40, 5, 2}, {125, 40, 5, 2}, // the gap between them: the nearer
		{60, 67, 5, 1}, {60, 72, 5, 0}, // below one: within the slop, then beyond it
		{122, 40, 0, 0}, // no slop, as before
	} {
		z, _, in := mediaHit(zones, card, c.x, c.y, c.slop)
		got := 0
		if z.kind == mediaPartVolume {
			got = z.index
		}
		if !in || got != c.want {
			t.Errorf("(%d,%d) slop %d: part %d (on the sheet %v), want %d", c.x, c.y, c.slop, got, in, c.want)
		}
	}
}
