//go:build !dot && !spot

package tessera

import "testing"

// A screen of any size gets the grid a Show of that size has, and a tablet's, either way up.
func TestGridForSize(t *testing.T) {
	for _, c := range []struct {
		w, h int
		want grid
	}{
		{960, 480, gridFor(false, false)},
		{1280, 800, gridFor(true, false)},
		{1920, 1080, gridFor(false, true)},
		{2560, 1600, grid{2560, 1600, 4, 3}},
		{1600, 2560, grid{1600, 2560, 3, 4}},
		{800, 1280, grid{800, 1280, 3, 4}},
		{1024, 600, grid{1024, 600, 5, 3}},
		{6000, 600, grid{6000, 600, 8, 3}},
		{500, 500, grid{500, 500, 2, 2}},
	} {
		if got := gridForSize(c.w, c.h); got != c.want {
			t.Errorf("gridForSize(%d, %d) = %v, want %v", c.w, c.h, got, c.want)
		}
	}
}
