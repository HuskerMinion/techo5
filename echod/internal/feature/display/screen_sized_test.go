//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

// A picture the screen's size is drawn as it is; one made at another size (a panel's photo from before
// its screen said its size) is scaled to cover the screen rather than drawn into a corner, and scaled
// once for as long as it stays up.
func TestScreenSizedScalesOnlyAMismatch(t *testing.T) {
	var k screenSized
	same := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	if got := k.of(same, 1280, 800); got != same {
		t.Error("a picture the screen's size was copied")
	}
	if k.of(nil, 1280, 800) != nil {
		t.Error("no picture became one")
	}

	small := image.NewRGBA(image.Rect(0, 0, 960, 480))
	draw.Draw(small, small.Rect, image.NewUniform(color.RGBA{200, 40, 40, 255}), image.Point{}, draw.Src)
	got := k.of(small, 1280, 800)
	if got.Rect != image.Rect(0, 0, 1280, 800) {
		t.Fatalf("scaled to %v, want 1280x800", got.Rect)
	}
	if c := got.RGBAAt(1270, 790); c.R < 180 {
		t.Errorf("bottom right is %v, want the picture there", c)
	}
	if k.of(small, 1280, 800) != got {
		t.Error("the same picture was scaled again")
	}
}
