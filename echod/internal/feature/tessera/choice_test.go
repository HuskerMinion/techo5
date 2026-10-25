//go:build !dot && !spot

package tessera

import (
	"slices"
	"testing"
)

// The Show 5's choices keep cells of 120 pixels or more and not much longer one way than the other.
func TestGridChoices(t *testing.T) {
	got := gridChoices(grid{960, 480, 4, 2})
	for _, want := range []string{"4x2", "6x3", "8x4", "3x2"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is not offered on a Show 5: %v", want, got)
		}
	}
	for _, not := range []string{"8x1", "1x4", "9x2", "6x5"} {
		if slices.Contains(got, not) {
			t.Errorf("%s is offered on a Show 5: %v", not, got)
		}
	}
}

// A grid chosen is in force where the screen has room for it, and the screen's own otherwise.
func TestChosen(t *testing.T) {
	base := grid{960, 480, 4, 2}
	if g := chosen(base, "6x3"); g != (grid{960, 480, 6, 3}) {
		t.Errorf("6x3 gave %v", g)
	}
	for _, c := range []string{"", "Automatic (4x2)", "9x9", "6 x 3"} {
		if g := chosen(base, c); g != base {
			t.Errorf("%q gave %v, want the screen's own", c, g)
		}
	}
}
