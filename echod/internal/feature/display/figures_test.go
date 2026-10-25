//go:build !dot

package display

import (
	"testing"
	"time"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
)

// The Stack keeps the hours' 0 on a 12-hour clock too, and counts them as the clock format does.
func TestStackDigits(t *testing.T) {
	t.Cleanup(func() { clock24.Store(false) })
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.UTC)
	for _, c := range []struct {
		h24  bool
		at   time.Time
		want [4]int
	}{
		{false, at, [4]int{0, 2, 0, 7}},
		{true, at, [4]int{1, 4, 0, 7}},
		{false, at.Add(-14 * time.Hour), [4]int{1, 2, 0, 7}},
		{true, at.Add(-14 * time.Hour), [4]int{0, 0, 0, 7}},
	} {
		clock24.Store(c.h24)
		if got := stackDigits(c.at); got != c.want {
			t.Errorf("%s, 24 hours %v: %v, want %v", c.at.Format("15:04"), c.h24, got, c.want)
		}
	}
}

// The Overlap's figures reach over each other within the hours and the minutes, and stand apart
// between them; a figure's shape is kept, and a zero with a slash through it has none in its middle.
func TestOverlapFigures(t *testing.T) {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		t.Fatal(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 200, DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	at, w, h := overlapRow(face, [4]int{2, 2, 0, 7})
	two, zero := inkOf(face, "2").Dx(), inkOf(face, "0").Dx()
	if at[1] >= two {
		t.Errorf("the hours' second figure starts at %d, not over the first (%d wide)", at[1], two)
	}
	if at[2] <= at[1]+two+h/4 {
		t.Errorf("the minutes start at %d, over the hours (which end at %d)", at[2], at[1]+two)
	}
	if at[3] >= at[2]+zero {
		t.Errorf("the minutes' second figure starts at %d, not over the first (ending at %d)", at[3], at[2]+zero)
	}
	if w <= 0 || h != figureHeight(face) {
		t.Errorf("the row is %d by %d", w, h)
	}
	var g figureShapes
	m := g.mask(face, 0)
	if g.mask(face, 0) != m {
		t.Error("the same figure was drawn twice")
	}
	if mid := m.Rect.Min.Add(m.Rect.Max).Div(2); m.AlphaAt(mid.X, mid.Y).A > 96 {
		t.Errorf("the zero has ink in its middle: %d", m.AlphaAt(mid.X, mid.Y).A)
	}
	if m.Rect != inkOf(face, "0") {
		t.Errorf("the zero is %v, want the zero's place %v", m.Rect, inkOf(face, "0"))
	}
}
