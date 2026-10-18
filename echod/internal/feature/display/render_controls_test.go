//go:build !dot && !spot

package display

import (
	"image"
	"testing"
)

// The star and the strip's cross come from the icon font as well, and the ink of a symbol sits at its own
// height above the baseline, so one lifted box cannot centre them all: measured, the cross was two pixels
// high and the star one. mdiIconIn asks the face for the glyph's bounds instead, and these two keep it that
// way. The transport marks are covered by TestTheTransportIconSitsOnItsButton.
func TestTheStarAndCrossSitOnTheirButtons(t *testing.T) {
	r := testRenderer()
	_, _, _, closeX := r.stripButtons()
	fav := r.favButton()

	for _, c := range []struct {
		name string
		b    image.Rectangle
		draw func(rr *renderer)
	}{
		{"close", closeX, func(rr *renderer) { rr.iconIn(closeX, "close", closeIcon, cream) }},
		{"star", fav, func(rr *renderer) { rr.iconIn(fav, "star", transportIcon, amber) }},
	} {
		with := testRenderer()
		with.buttonFace(c.b, float64(c.b.Dy())/2, btnSecondary)
		c.draw(with)
		bare := testRenderer()
		bare.buttonFace(c.b, float64(c.b.Dy())/2, btnSecondary)
		ink, ok := diffBox(with, bare)
		if !ok {
			t.Fatalf("the %s drew nothing", c.name)
		}
		cx, cy := c.b.Min.X+c.b.Dx()/2, c.b.Min.Y+c.b.Dy()/2
		dx, dy := (ink.Min.X+ink.Max.X)/2-cx, (ink.Min.Y+ink.Max.Y)/2-cy
		t.Logf("%-6s button %v centre %d,%d | ink %v | off %+d,%+d", c.name, c.b, cx, cy, ink, dx, dy)
		if !ink.In(c.b) {
			t.Errorf("the %s icon %v is not inside its button %v", c.name, ink, c.b)
		}
		if dx < -2 || dx > 2 {
			t.Errorf("the %s icon is %d px off the middle of its button", c.name, dx)
		}
		// One pixel is what is left of the glyph's own half-pixel once the baseline has to be a whole one.
		if dy < -1 || dy > 1 {
			t.Errorf("the %s icon is %d px off the middle of its button, vertically", c.name, dy)
		}
	}
}
