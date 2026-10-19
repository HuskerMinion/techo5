//go:build spot

package display

import (
	"image"
	"testing"
)

// spotDiffBox is diffBox for the round renderer: the pixels two faces disagree on, which is the
// label drawn over the button under it.
func spotDiffBox(a, b *roundRenderer) (image.Rectangle, bool) {
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

// The round camera page's Talk and sound controls take the same buttonFace path as the Show's: the
// same helper, the round panel's own faces, and the label in the middle of the bar it was measured
// into. The talk's live red keeps its fill (roundButton), the color that says the room is heard.
func TestTheRoundCameraControlsSitOnTheirButtons(t *testing.T) {
	for _, c := range []struct {
		name string
		live bool
	}{
		{"Mute", false},
		{"Unmute", false},
		{"Talk", false},
		{"End talk  1:45", true},
	} {
		ping := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
		b := cameraSoundBox(ping.width(ping.label, c.name) + 24)
		with := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
		with.cameraControl(b, c.name, c.live)
		without := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
		if c.live {
			without.roundButton(b, float64(b.Dy())/2, talkLive)
		} else {
			without.buttonFace(b, float64(b.Dy())/2, btnSecondary)
		}
		ink, ok := spotDiffBox(with, without)
		if !ok {
			t.Fatalf("camera %s drew no label at all", c.name)
		}
		if !ink.In(b) {
			t.Errorf("camera %s label %v is not inside its button %v", c.name, ink, b)
		}
		cx, cy := b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2
		dx, dy := (ink.Min.X+ink.Max.X)/2-cx, (ink.Min.Y+ink.Max.Y)/2-cy
		t.Logf("%-20s button %v center %d,%d | ink %v | off %+d,%+d", "spot camera "+c.name, b, cx, cy, ink, dx, dy)
		if dx < -2 || dx > 2 {
			t.Errorf("camera %s label is %d px off the middle of its button", c.name, dx)
		}
		if dy < -2 || dy > 2 {
			t.Errorf("camera %s label is %d px off the middle of its button, vertically", c.name, dy)
		}
	}
}
