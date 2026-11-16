//go:build spot

package display

import (
	"image"
	"testing"
)

// The settings card's buttons keep inside the round glass at their height, however long their words:
// a long pair gives up the end of its longest word instead of running off both sides.
func TestTheCardsButtonsFitTheGlass(t *testing.T) {
	for _, actions := range [][]headerAction{
		{{id: "add", label: "Add"}},
		{{id: "cancel"}},
		{{id: "x", label: "Einstellungen zurücksetzen und neu starten", style: btnPrimary}},
	} {
		r := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, 480, 480)))
		r.cardButtons(actions)
		left, right := 480, 0
		n := 0
		for _, z := range r.pending {
			if z.kind == zoneAction {
				left, right = min(left, z.r.Min.X+4), max(right, z.r.Max.X-4)
				n++
			}
		}
		if n < 2 {
			t.Fatalf("%v: %d buttons drawn", actions, n)
		}
		half := chord(spotButtonsY) / 2
		if left < center-half || right > center+half {
			t.Errorf("%v: the buttons run %d..%d, off glass that is %d..%d there", actions, left, right, center-half, center+half)
		}
	}
}
