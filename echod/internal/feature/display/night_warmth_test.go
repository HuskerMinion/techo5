//go:build !dot && !spot

package display

import (
	"math"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/screen"
)

func TestWarmthTintIsNoneAtTheWhiteOfThePanel(t *testing.T) {
	got := warmthTint(6500)
	for i, v := range got {
		if math.Abs(v-1) > 1e-9 {
			t.Fatalf("channel %d at 6500 K: %v, want 1", i, v)
		}
	}
}

// A warmer white keeps red whole and takes more from blue than from green, the more so the warmer.
func TestWarmthTintTakesBlueFirst(t *testing.T) {
	warm, warmer := warmthTint(3400), warmthTint(2700)
	t.Logf("3400 K %.3f, 2700 K %.3f", warm, warmer)
	for _, c := range []screen.Tint{warm, warmer} {
		if c[0] != 1 || !(c[1] < 1 && c[2] < c[1] && c[2] > 0.3) {
			t.Fatalf("tint %v: want red 1 > green > blue > 0.3", c)
		}
	}
	if !(warmer[1] < warm[1] && warmer[2] < warm[2]) {
		t.Fatalf("2700 K %v is not warmer than 3400 K %v", warmer, warm)
	}
}
