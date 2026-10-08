//go:build !dot && !spot

package display

import (
	"image"
	"testing"
)

// A square photo in a 4:3 frame loses a strip top and bottom rather than being squeezed, a wide
// one loses its sides, and one already the frame's shape is used whole. One far narrower or flatter
// than the frame keeps at least a pixel, so it still draws.
func TestAPictureCoversItsFrame(t *testing.T) {
	frame := image.Rect(0, 0, 400, 300)
	for _, c := range []struct{ src, want image.Rectangle }{
		{image.Rect(0, 0, 512, 512), image.Rect(0, 64, 512, 448)},
		{image.Rect(0, 0, 800, 300), image.Rect(200, 0, 600, 300)},
		{image.Rect(0, 0, 800, 600), image.Rect(0, 0, 800, 600)},
		{image.Rect(0, 0, 0, 0), image.Rect(0, 0, 0, 0)},
		{image.Rect(0, 0, 1, 1000), image.Rect(0, 499, 1, 500)},
		{image.Rect(0, 0, 1000, 1), image.Rect(499, 0, 500, 1)},
	} {
		if got := cover(c.src, frame); got != c.want {
			t.Errorf("cover(%v) = %v, want %v", c.src, got, c.want)
		}
	}
}
