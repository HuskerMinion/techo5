//go:build !dot && !spot

package display

import (
	"image"
	"testing"
)

// labelOnButton is the measure these tests take of a converted control: draw the control with its
// words, draw the same button bare, and the pixels between the two are the words. Their box has to
// sit in the middle of the button, or the control is not the program's button with a label in it.
func labelOnButton(t *testing.T, name string, b image.Rectangle, draw, bare func(*renderer)) {
	t.Helper()
	if b.Empty() {
		t.Fatalf("%s has no button to measure against", name)
	}
	with := testRenderer()
	draw(with)
	without := testRenderer()
	bare(without)
	ink, ok := diffBox(with, without)
	if !ok {
		t.Fatalf("%s drew no label at all", name)
	}
	if !ink.In(b) {
		t.Errorf("%s label %v is not inside its button %v", name, ink, b)
	}
	cx, cy := b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2
	dx, dy := (ink.Min.X+ink.Max.X)/2-cx, (ink.Min.Y+ink.Max.Y)/2-cy
	t.Logf("%-24s button %v center %d,%d | ink %v | off %+d,%+d", name, b, cx, cy, ink, dx, dy)
	// Two pixels either way: a glyph's own ink does not fill its line box, so a word with a
	// descender ("Today") or the arrow glyphs land a little off; the box itself is the middle.
	if dx < -2 || dx > 2 {
		t.Errorf("%s label is %d px off the middle of its button", name, dx)
	}
	if dy < -2 || dy > 2 {
		t.Errorf("%s label is %d px off the middle of its button, vertically", name, dy)
	}
}

func TestTheCameraControlsSitOnTheirButtons(t *testing.T) {
	for _, c := range []struct {
		name string
		live bool
	}{
		{"Mute", false},
		{"Unmute", false},
		{"Talk", false},
		{"End talk  1:45", true},
	} {
		r := testRenderer()
		b := r.cameraSoundBox(c.name)
		labelOnButton(t, "camera "+c.name, b,
			func(rr *renderer) { rr.cameraControl(b, c.name, c.live) },
			func(rr *renderer) {
				if c.live {
					rr.roundButton(b, float64(b.Dy())/2, talkLive)
					return
				}
				rr.buttonFace(b, float64(b.Dy())/2, btnSecondary)
			})
	}
}

func TestTheCalendarsHeaderButtonsSitOnTheirButtons(t *testing.T) {
	for _, label := range []string{"Done", "Today", "‹", "›"} {
		r := testRenderer()
		right := r.w - r.margin
		left := r.calButton(label, right, calDone) + r.s(14)
		b := image.Rect(left, r.margin-r.s(8), right, r.margin+r.s(40))
		labelOnButton(t, "calendar "+label, b,
			func(rr *renderer) { rr.calButton(label, right, calDone) },
			func(rr *renderer) { rr.buttonFace(b, float64(b.Dy())/2, btnSecondary) })
	}
}

func TestTheCalendarsCloseAndDoneSitOnTheirButtons(t *testing.T) {
	// The window's Close and the calendars list's Done are the same helper as the header's buttons,
	// in the boxes those pages give them.
	for _, c := range []struct {
		name string
		b    image.Rectangle
	}{
		{"calendar Close", image.Rect(700, 300, 828, 348)},
		{"calendar list Done", image.Rect(700, 100, 808, 148)},
	} {
		label := "Close"
		if c.name == "calendar list Done" {
			label = "Done"
		}
		labelOnButton(t, c.name, c.b,
			func(rr *renderer) { rr.calButtonAt(c.b, label) },
			func(rr *renderer) { rr.buttonFace(c.b, float64(c.b.Dy())/2, btnSecondary) })
	}
}

func TestTheCalendarsMoreChipSitsOnItsButton(t *testing.T) {
	r := testRenderer()
	const x, cy, n = 300, 180, 4
	_, h := r.chipFace(false)
	b := image.Rect(x, cy-h/2, x+r.moreChipWidth(n), cy-h/2+h)
	labelOnButton(t, "calendar +N", b,
		func(rr *renderer) { rr.moreChip(n, x, cy) },
		func(rr *renderer) { rr.buttonFace(b, float64(b.Dy())/2, btnSecondary) })
}

func TestTheCalendarsCompactButtonsSitOnTheirButtons(t *testing.T) {
	// The day's Later pill and the week's off-hours markers keep their measured boxes, so their
	// words stay in the micro face: the button face would not fit. The shape is still the program's.
	for _, c := range []struct {
		name  string
		b     image.Rectangle
		label string
	}{
		{"calendar Later", image.Rect(500, 400, 820, 430), "▼  Later: Dentist · 3:00 PM"},
		{"calendar week marker", image.Rect(120, 300, 236, 318), "▲ 2 more"},
	} {
		labelOnButton(t, c.name, c.b,
			func(rr *renderer) { rr.calMicroButton(c.b, c.label) },
			func(rr *renderer) { rr.buttonFace(c.b, float64(c.b.Dy())/2, btnSecondary) })
	}
}

func TestThePinCancelSitsOnItsButton(t *testing.T) {
	r := testRenderer()
	b := r.pinCancel()
	labelOnButton(t, "pin Cancel", b,
		func(rr *renderer) { rr.pinButton(b, "Cancel") },
		func(rr *renderer) { rr.buttonFace(b, float64(b.Dy())/2, btnSecondary) })
}
