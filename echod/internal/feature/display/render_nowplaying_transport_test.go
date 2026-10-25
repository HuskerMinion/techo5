//go:build !dot && !spot

package display

import (
	"image"
	"testing"
)

// transportButton draws the button the rest of the program draws, with the icon font's mark inside it.
// The mark has to land on the middle of whatever button it is handed: the page's are wide pills and the
// strip's are circles, and both come through the same call.

// diffBox is the box of the pixels two panels disagree on: the icon, told from the button under it
// without the test having to know either color.
func diffBox(a, b *renderer) (image.Rectangle, bool) {
	var box image.Rectangle
	found := false
	bounds := a.dst.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ar, ag, ab, aa := a.dst.At(x, y).RGBA()
			br, bg, bb, ba := b.dst.At(x, y).RGBA()
			if ar == br && ag == bg && ab == bb && aa == ba {
				continue
			}
			p := image.Rect(x, y, x+1, y+1)
			if !found {
				box, found = p, true
			} else {
				box = box.Union(p)
			}
		}
	}
	return box, found
}

func TestTheTransportIconSitsOnItsButton(t *testing.T) {
	buttons := map[string]image.Rectangle{
		"the page's pill":    image.Rect(300, 200, 396, 254),
		"the strip's circle": image.Rect(400, 300, 466, 366),
	}
	for _, icon := range []string{"skip-previous", "skip-next", "play", "pause"} {
		// The middle button is the accent one, the others take the theme's surface, as in the media
		// sheet: the button's style must not move the mark off the middle.
		accent := icon == "play" || icon == "pause"
		style := btnSecondary
		if accent {
			style = btnPrimary
		}
		for where, b := range buttons {
			with := testRenderer()
			with.transportButton(b, icon, accent)
			bare := testRenderer()
			bare.buttonFace(b, float64(b.Dy())/2, style)

			mark, ok := diffBox(with, bare)
			if !ok {
				t.Fatalf("the %s icon drew nothing at all in %s", icon, where)
			}
			dx := (mark.Min.X+mark.Max.X)/2 - (b.Min.X + b.Dx()/2)
			dy := (mark.Min.Y+mark.Max.Y)/2 - (b.Min.Y + b.Dy()/2)
			// The numbers, so a shift of one or two pixels can be told from a mistake: mdiIcon puts the
			// baseline at the bottom of the box it is given, not at the bottom of the glyph's own ink.
			t.Logf("%-13s in %-18s button %v center %d,%d | ink %v | off %+d,%+d",
				icon, where, b, b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2, mark, dx, dy)
			if !mark.In(b) {
				t.Errorf("the %s icon %v is not inside %s %v", icon, mark, where, b)
			}
			// Sideways two is the glyph's own doing — MDI's play sits right of its em box's middle — and
			// is left as it is. Vertically one is the whole tolerance: placing the glyph by its own ink
			// (mdiIconIn) is what takes out the pixels mdiIcon's baseline leaves in, and this is the test
			// that says so.
			if dx < -2 || dx > 2 {
				t.Errorf("the %s icon is %d px off the middle of %s", icon, dx, where)
			}
			if dy < -1 || dy > 1 {
				t.Errorf("the %s icon is %d px off the middle of %s, vertically", icon, dy, where)
			}
		}
	}
}

// The icons are the font's, so they cannot be drawn at a size the button has no room for: whatever the
// panel, the mark is smaller than the button it sits in and does not reach its edge.
func TestTheTransportIconFitsItsButton(t *testing.T) {
	for _, panel := range []image.Point{{X: 960, Y: 480}, {X: 1280, Y: 800}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.X, panel.Y)))
		for _, b := range []image.Rectangle{r.doneButton(), r.favButton()} {
			bare := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.X, panel.Y)))
			bare.buttonFace(b, float64(b.Dy())/2, btnSecondary)
			with := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.X, panel.Y)))
			with.transportButton(b, "skip-next", false)
			mark, ok := diffBox(with, bare)
			if !ok {
				t.Fatalf("nothing was drawn in %v on %dx%d", b, panel.X, panel.Y)
			}
			if mark.Dx() >= b.Dx() || mark.Dy() >= b.Dy() {
				t.Errorf("the mark %v fills the button %v on %dx%d", mark, b, panel.X, panel.Y)
			}
		}
	}
}

// The icons are the font's, and a name the font does not know draws nothing at all: mdiIcon gives up
// quietly when mdi.Rune has no glyph for it. Every name the program asks for has to leave ink behind.
func TestTheIconNamesExist(t *testing.T) {
	for _, name := range []string{"star", "star-outline", "close", "play", "pause", "skip-previous", "skip-next",
		"chevron-left", "chevron-right"} {
		r := testRenderer()
		r.mdiIcon(name, 100, 100, transportIcon, cream)
		if _, drew := diffBox(r, testRenderer()); !drew {
			t.Errorf("the font has no %q, so that control would be invisible", name)
		}
	}
}
