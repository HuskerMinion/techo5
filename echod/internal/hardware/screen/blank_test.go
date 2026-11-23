//go:build !dot && !panel

package screen

import (
	"image"
	"image/color"
	"testing"
)

// The frame Close leaves on the panel is black and opaque, whatever was drawn before it (#560).
func TestTheLastFrameIsBlack(t *testing.T) {
	c := image.NewRGBA(image.Rect(0, 0, 5, 3))
	for i := range c.Pix {
		c.Pix[i] = 0xc8
	}
	blank(c)
	for y := range 3 {
		for x := range 5 {
			if got := c.RGBAAt(x, y); got != (color.RGBA{0, 0, 0, 0xff}) {
				t.Fatalf("pixel %d,%d is %v, not black", x, y, got)
			}
		}
	}
}
