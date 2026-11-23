//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The alert page's ‹ › and Close are where a finger lands on them, at the Show 5's size, the Show 8's
// and the Skylight's: on the screen, apart, in the order the calendar's header has them, and the
// arrows only while there is more than one alert to go to.
func TestTheAlertPageIsTappedWhereItIsDrawn(t *testing.T) {
	for _, size := range []image.Point{{960, 480}, {1280, 800}, {skylightWide, skylightHigh}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, size.X, size.Y)))
		prev, next, closeB := r.alertPrevButton(), r.alertNextButton(), r.alertCloseButton()
		for name, b := range map[string]image.Rectangle{"‹": prev, "›": next, "Close": closeB} {
			if b.Empty() || !b.In(image.Rect(0, 0, size.X, size.Y)) {
				t.Errorf("%v: %s at %v is not on the screen", size, name, b)
			}
		}
		if prev.Max.X > next.Min.X || next.Max.X > closeB.Min.X {
			t.Errorf("%v: ‹ %v, › %v and Close %v overlap or are out of order", size, prev, next, closeB)
		}
		if prev.Min.Y != closeB.Min.Y || next.Max.Y != closeB.Max.Y {
			t.Errorf("%v: the arrows %v %v are not on Close's row %v", size, prev, next, closeB)
		}
		mid := func(b image.Rectangle) image.Point { return b.Min.Add(b.Size().Div(2)) }
		for _, tc := range []struct {
			at   image.Point
			n    int
			want alertTap
		}{
			{mid(prev), 2, alertTapPrev},
			{mid(next), 2, alertTapNext},
			{mid(closeB), 2, alertTapClose},
			{mid(prev), 1, alertTapNone},
			{mid(next), 1, alertTapNone},
			{mid(closeB), 1, alertTapClose},
			{image.Pt(size.X/2, size.Y/2), 2, alertTapNone},
		} {
			if got := r.alertTapped(tc.at, tc.n); got != tc.want {
				t.Errorf("%v: a tap at %v with %d alerts lands on %d, want %d", size, tc.at, tc.n, got, tc.want)
			}
		}
	}
}

// The arrows go round: past the last is the first, before the first the last.
func TestAlertStepGoesRound(t *testing.T) {
	for _, tc := range []struct{ i, n, step, want int }{
		{0, 3, 1, 1}, {2, 3, 1, 0}, {0, 3, -1, 2}, {1, 3, -1, 0}, {0, 1, 1, 0}, {0, 1, -1, 0},
	} {
		if got := alertStep(tc.i, tc.n, tc.step); got != tc.want {
			t.Errorf("alertStep(%d, %d, %+d) = %d, want %d", tc.i, tc.n, tc.step, got, tc.want)
		}
	}
}

// The page's buttons are the program's buttons, their marks and words in the middle of them.
func TestTheAlertPagesButtonsSitOnTheirButtons(t *testing.T) {
	r := testRenderer()
	for _, c := range []struct {
		icon string
		b    image.Rectangle
	}{{"chevron-left", r.alertPrevButton()}, {"chevron-right", r.alertNextButton()}} {
		labelOnButtonWithin(t, "alert "+c.icon, c.b, 1,
			func(rr *renderer) { rr.calIconButtonAt(c.b, c.icon) },
			func(rr *renderer) { rr.buttonFace(c.b, float64(c.b.Dy())/2, btnSecondary) })
	}
	b := r.alertCloseButton()
	labelOnButton(t, "alert Close", b,
		func(rr *renderer) { rr.labelButton(b, "Close", btnSecondary) },
		func(rr *renderer) { rr.buttonFace(b, float64(b.Dy())/2, btnSecondary) })
}

// One alert draws no arrows; two draw both.
func TestTheAlertPageDrawsItsArrowsOnlyForMoreThanOne(t *testing.T) {
	a := home.Alert{Event: "Wind Advisory", Color: color.RGBA{255, 200, 0, 255}, Description: "Gusts to 50 mph."}
	now := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	for _, n := range []int{1, 2} {
		r := testRenderer()
		here := []home.Alert{a, a}[:n]
		r.alertPage(scene{now: now, alerts: home.AlertView{Here: here}})
		for _, b := range []image.Rectangle{r.alertPrevButton(), r.alertNextButton()} {
			drawn := false
			for y := b.Min.Y; y < b.Max.Y && !drawn; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					if r.dst.RGBAAt(x, y) != walnut {
						drawn = true
						break
					}
				}
			}
			if drawn != (n > 1) {
				t.Errorf("%d alerts: something drawn at the arrow %v is %v, want %v", n, b, drawn, n > 1)
			}
		}
	}
}
