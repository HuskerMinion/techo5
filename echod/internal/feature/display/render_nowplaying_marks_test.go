//go:build !dot && !spot

package display

import (
	"image"
	"testing"
)

// The transport marks are hand-drawn and positioned from the middle of their button. The parts are of
// different widths, which is what went wrong before: a mark of a bar and a triangle was drawn with a gap
// twice the width of the bar, and the pause and play marks sat left of center on the now-playing page,
// because one guessed offset was used for all of them. These tests hold the three promises a mark makes:
// one height, one gap between its parts, and a middle that is the middle. They are pixel tests because
// the marks *are* pixels; nothing above them knows their geometry.

// transportMarks are the four marks, drawn the way a caller draws them. gapped says whether a mark is
// made of more than one piece, and so has a gap between them.
var transportMarks = []struct {
	name   string
	draw   func(r *renderer, cx, top int)
	gapped bool
}{
	{"back", func(r *renderer, cx, top int) { r.markBack(cx, top) }, true},
	{"next", func(r *renderer, cx, top int) { r.markNext(cx, top) }, true},
	{"play", func(r *renderer, cx, top int) { r.markPlay(cx, top) }, false},
	{"pause", func(r *renderer, cx, top int) { r.markPause(cx, top) }, true},
}

// markInk draws one mark on a blank panel and returns the renderer and the box its ink covers.
func markInk(t *testing.T, cx, top int, draw func(r *renderer, cx, top int)) (*renderer, image.Rectangle) {
	t.Helper()
	r := testRenderer()
	draw(r, cx, top)
	b, ok := inkBounds(r)
	if !ok {
		t.Fatalf("the mark drew nothing at %d,%d", cx, top)
	}
	return r, b
}

// inkBounds is the box of everything drawn on the panel, and whether anything was. A blank panel is
// transparent, so a pixel with any alpha at all is ink.
func inkBounds(r *renderer) (image.Rectangle, bool) {
	var b image.Rectangle
	any := false
	bounds := r.dst.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, a := r.dst.At(x, y).RGBA(); a != 0 {
				p := image.Rect(x, y, x+1, y+1)
				if !any {
					b, any = p, true
				} else {
					b = b.Union(p)
				}
			}
		}
	}
	return b, any
}

// widestGap is the widest run of columns with no ink at all inside b. That is the space between the parts
// of a mark: the gap in next, the one between pause's bars, and none inside a bar or a triangle.
func widestGap(r *renderer, b image.Rectangle) int {
	widest, run := 0, 0
	for x := b.Min.X; x < b.Max.X; x++ {
		empty := true
		for y := b.Min.Y; y < b.Max.Y; y++ {
			if _, _, _, a := r.dst.At(x, y).RGBA(); a != 0 {
				empty = false
				break
			}
		}
		if empty {
			run++
			widest = max(widest, run)
		} else {
			run = 0
		}
	}
	return widest
}

// A mark made of a bar and a triangle looked broken because the bar was 36 rows high and the triangle 30:
// the bar stood proud of it top and bottom, and the triangle sat three rows high beside it.
func TestAMarkIsOneHeight(t *testing.T) {
	const cx, top = 200, 100
	for _, m := range transportMarks {
		r, b := markInk(t, cx, top, m.draw)
		if got, want := b.Dy(), r.s(markHeight); got != want {
			t.Errorf("the %s mark is %d rows high, want the bar's %d", m.name, got, want)
		}
		if got := b.Min.Y; got != top {
			t.Errorf("the %s mark starts at row %d, want %d", m.name, got, top)
		}
	}
}

// Pause and play sat left of center, and next right of it, wherever one offset was used for marks of
// different widths.
func TestAMarkSitsOnItsMiddle(t *testing.T) {
	const cx, top = 200, 100
	for _, m := range transportMarks {
		_, b := markInk(t, cx, top, m.draw)
		// A mark of even width covers one pixel more on one side of the middle than on the other.
		if off := b.Min.X + b.Max.X - 2*cx; off < 0 || off > 2 {
			t.Errorf("the %s mark is off its middle: it covers %v around %d", m.name, b, cx)
		}
	}
}

// The next mark's bar used to be laid down 22 px past the triangle's edge, twice the bar's own width,
// while the same pair in the previous mark had 3: the two did not look like a pair at all.
func TestThePartsOfAMarkKeepTheGap(t *testing.T) {
	const cx, top = 200, 100
	for _, m := range transportMarks {
		if !m.gapped {
			continue
		}
		r, b := markInk(t, cx, top, m.draw)
		if got, want := widestGap(r, b), r.s(markGap); got != want {
			t.Errorf("the %s mark leaves %d empty columns inside it, want markGap's %d", m.name, got, want)
		}
	}
}
