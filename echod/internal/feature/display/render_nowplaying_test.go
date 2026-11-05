//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"testing"
)

// The three transport buttons are drawn from the panel's own size, and their rectangles are the tap
// regions as well, so an overlap is a button that cannot be pressed and an overflow is one that cannot
// be seen. The Show 8 is why this is a table: the now-playing page is one file for both panels, and a row
// sized for the Show 5 huddles in the middle of a wider screen.
func TestTheTransportButtonsFitTheirPanel(t *testing.T) {
	for _, panel := range []image.Point{{X: 960, Y: 480}, {X: 1280, Y: 800}} {
		t.Run(fmt.Sprintf("%dx%d", panel.X, panel.Y), func(t *testing.T) {
			screen := image.Rect(0, 0, panel.X, panel.Y)
			r := newRenderer(image.NewRGBA(screen))
			back, play, next := r.transportButtons()

			for name, b := range map[string]image.Rectangle{"back": back, "play": play, "next": next} {
				if !b.In(screen) {
					t.Errorf("the %s button %v is not on the panel", name, b)
				}
			}
			if back.Overlaps(play) || play.Overlaps(next) || back.Overlaps(next) {
				t.Errorf("the buttons overlap: back %v, play %v, next %v", back, play, next)
			}
			if back.Max.X > play.Min.X || play.Max.X > next.Min.X {
				t.Errorf("the buttons are out of order: back %v, play %v, next %v", back, play, next)
			}
			if back.Min.Y != play.Min.Y || play.Min.Y != next.Min.Y ||
				back.Dy() != play.Dy() || play.Dy() != next.Dy() {
				t.Errorf("the buttons are not one row of one size: back %v, play %v, next %v", back, play, next)
			}
			if left, right := back.Min.X, screen.Max.X-next.Max.X; left != right {
				t.Errorf("%d px on one side of the row and %d on the other", left, right)
			}
			if got := play.Dx(); got != r.s(96) {
				t.Errorf("a button is %d wide, want %d on this panel", got, r.s(96))
			}
			if footer := panel.Y - r.s(26); next.Max.Y > footer {
				t.Errorf("the buttons reach %d, into the footer at %d", next.Max.Y, footer)
			}
		})
	}
}

func TestModernMusicControlsFitBesideTheCover(t *testing.T) {
	for _, panel := range []image.Point{{X: 960, Y: 480}, {X: 1280, Y: 800}, {X: 1920, Y: 1080}} {
		t.Run(fmt.Sprintf("%dx%d", panel.X, panel.Y), func(t *testing.T) {
			screen := image.Rectangle{Max: panel}
			r := newRenderer(image.NewRGBA(screen))
			cover, details := r.modernMusicLayout()
			back, play, next := r.musicButtons(true)
			if !cover.In(screen) || cover.Dx() != cover.Dy() || cover.Max.X >= details.Min.X {
				t.Fatalf("cover %v does not fit beside details %v", cover, details)
			}
			if cover.Overlaps(r.doneButton()) || cover.Overlaps(r.favButton()) {
				t.Errorf("cover %v obscures the footer", cover)
			}
			for _, b := range []image.Rectangle{back, play, next} {
				if !b.In(screen) || b.Min.X < details.Min.X || b.Max.X > details.Max.X {
					t.Errorf("button %v does not fit in the details column %v", b, details)
				}
				if b.Overlaps(r.doneButton()) || b.Overlaps(r.favButton()) {
					t.Errorf("button %v overlaps the footer", b)
				}
			}
			if back.Max.X > play.Min.X || play.Max.X > next.Min.X {
				t.Errorf("transport buttons overlap or are out of order: %v %v %v", back, play, next)
			}
		})
	}
}
