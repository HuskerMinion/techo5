//go:build !dot && !spot

package display

import (
	"image"
	"testing"
)

// labelOnButton is the measure these tests take of a converted control: draw the control with its
// words, draw the same button bare, and the pixels between the two are the words. Their box has to
// sit in the middle of the button, or the control is not the program's button with a label in it.
// Two pixels either way: a glyph's own ink does not fill its line box, so a word with a descender
// ("Today") lands a little off; the box itself is the middle.
func labelOnButton(t *testing.T, name string, b image.Rectangle, draw, bare func(*renderer)) {
	t.Helper()
	labelOnButtonWithin(t, name, b, 2, draw, bare)
}

// labelOnButtonWithin is labelOnButton with the tolerance the label's ink deserves: a word needs the
// two pixels its line box leaves, a mark centered by its own ink next to none.
func labelOnButtonWithin(t *testing.T, name string, b image.Rectangle, tol int, draw, bare func(*renderer)) {
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
	if dx < -tol || dx > tol {
		t.Errorf("%s label is %d px off the middle of its button, past the %d it is allowed", name, dx, tol)
	}
	if dy < -tol || dy > tol {
		t.Errorf("%s label is %d px off the middle of its button, vertically, past the %d it is allowed", name, dy, tol)
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
	for _, label := range []string{"Done", "Today"} {
		r := testRenderer()
		right := r.w - r.margin
		left := r.calButton(label, right, calDone) + r.s(14)
		b := image.Rect(left, r.margin-r.s(8), right, r.margin+r.s(40))
		labelOnButton(t, "calendar "+label, b,
			func(rr *renderer) { rr.calButton(label, right, calDone) },
			func(rr *renderer) { rr.buttonFace(b, float64(b.Dy())/2, btnSecondary) })
	}
}

// The header's two arrows are icon-font chevrons, drawn through the renderer's ink-centering path
// (iconIn) rather than by a font's baseline: nothing hangs above or below the ink, so it has to sit
// on the middle of its button within a pixel. Their boxes are still the ones the text arrows
// measured, so only the glyph changed.
func TestTheCalendarsArrowsSitOnTheirButtons(t *testing.T) {
	for _, c := range []struct{ icon, label string }{
		{"chevron-left", "‹"},
		{"chevron-right", "›"},
	} {
		r := testRenderer()
		right := r.w - r.margin
		left := r.calIconButton(c.icon, c.label, right, calPrev)
		if want := r.calButton(c.label, right, calPrev); left != want {
			t.Errorf("the %s button starts at %d, where the text arrow started at %d", c.icon, left, want)
		}
		b := image.Rect(left+r.s(14), r.margin-r.s(8), right, r.margin+r.s(40))
		labelOnButtonWithin(t, "calendar "+c.icon, b, 1,
			func(rr *renderer) { rr.calIconButton(c.icon, c.label, right, calPrev) },
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
			func(rr *renderer) { rr.labelButton(c.b, label, btnSecondary) },
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
		func(rr *renderer) { rr.labelButton(b, "Cancel", btnSecondary) },
		func(rr *renderer) { rr.buttonFace(b, float64(b.Dy())/2, btnSecondary) })
}
