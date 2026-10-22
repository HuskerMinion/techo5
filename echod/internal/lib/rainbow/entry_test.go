package rainbow

import "testing"

// TestBlurColsIsBlurRun checks the blur down the columns a row at a time gives the bytes the blur a
// column at a time does.
func TestBlurColsIsBlurRun(t *testing.T) {
	for _, sz := range [][3]int{{37, 23, 2}, {64, 40, 3}, {5, 3, 2}, {9, 30, 1}} {
		w, h, r := sz[0], sz[1], sz[2]
		a := make([]uint8, w*h*4)
		for i := range a {
			a[i] = uint8(i*131 + i/7)
		}
		b := append([]uint8(nil), a...)
		blurRun(3, a, h, w, r, w*4, 4)
		blurCols(3, b, w, h, r)
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("%dx%d r %d: byte %d is %d, not %d", w, h, r, i, b[i], a[i])
			}
		}
	}
}
