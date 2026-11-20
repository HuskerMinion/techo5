//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The photo countdown (#656) is a thin line along the very foot of the screensaver and nothing else:
// off, the screensaver is as it was; on, only the bottom rows change, filling from the left as the
// photo's time goes, and the photo still shows through the line. With SHOW_PREVIEW set, the Show 8's
// is written there.
func TestThePhotoCountdownOnTheShow(t *testing.T) {
	at := time.Date(2026, 10, 17, 9, 41, 0, 0, time.Local)
	photo := testPhoto("sky", show8Wide, show8High)
	draw := func(on bool, done float64) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, show8Wide, show8High))
		newRenderer(img).draw(scene{now: at, phase: "idle", slideshowScreensaver: photo, slideshowOverlay: config.SlideshowOverlaySmall,
			saverCountdown: on, saverDone: done})
		return img
	}
	off := draw(false, 0.5)
	if !same(off.Pix, draw(false, 0).Pix) {
		t.Fatal("off, how much of the time has gone still changed the screensaver")
	}
	half := draw(true, 0.5)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	top := show8High - max(r.s(countdownThick), 2)
	changed := false
	for y := 0; y < show8High; y++ {
		for x := 0; x < show8Wide; x++ {
			if off.RGBAAt(x, y) == half.RGBAAt(x, y) {
				continue
			}
			if y < top {
				t.Fatalf("the countdown changed (%d,%d), above its line at %d", x, y, top)
			}
			changed = true
		}
	}
	if !changed {
		t.Fatal("the countdown drew nothing")
	}
	// Filled to the middle: a point a third across is lit more than one two thirds across, which has only
	// the faint track the whole way across.
	y := show8High - 1
	lift := func(x int) int {
		a, b := half.RGBAAt(x, y), off.RGBAAt(x, y)
		return int(a.R) + int(a.G) + int(a.B) - int(b.R) - int(b.G) - int(b.B)
	}
	if filled, track := lift(show8Wide/3), lift(show8Wide*2/3); filled <= track || track <= 0 {
		t.Errorf("a third across lifted %d, two thirds across %d: want the fill brighter than the track, and a track", filled, track)
	}
	if half.RGBAAt(show8Wide/3, y) == cream {
		t.Error("the line is solid: the photo does not show through it")
	}
	if same(draw(true, 0.25).Pix, half.Pix) {
		t.Error("the line does not grow with the time gone")
	}

	if dir := os.Getenv("SHOW_PREVIEW"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "saver-countdown-show8.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, draw(true, 0.4)); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
