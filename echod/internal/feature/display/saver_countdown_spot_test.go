//go:build spot

package display

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The round screensaver's photo countdown (#656) is a thin ring at the very edge of the face and nothing
// else: off, the screensaver is as it was; on, only the edge changes, filling clockwise from the top as
// the photo's time goes, and the photo still shows through it. With SPOT_PREVIEW set, it is written there.
func TestThePhotoCountdownOnTheSpot(t *testing.T) {
	at := time.Date(2026, 10, 17, 9, 41, 0, 0, time.Local)
	photo := testPhoto("sky", side, side)
	draw := func(on bool, done float64) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		newRoundRenderer(img).draw(roundScene{now: at, phase: "idle", slideshowScreensaver: photo, slideshowOverlay: config.SlideshowOverlaySmall,
			saverCountdown: on, saverDone: done})
		return img
	}
	off := draw(false, 0.5)
	if !bytes.Equal(off.Pix, draw(false, 0).Pix) {
		t.Fatal("off, how much of the time has gone still changed the screensaver")
	}
	half := draw(true, 0.5)
	inner := float64(rimOut - countdownWidth - countdownEdge - 1) // the ring's dark, soft inner edge
	changed := false
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			if off.RGBAAt(x, y) == half.RGBAAt(x, y) {
				continue
			}
			if d := math.Hypot(float64(x)+0.5-center, float64(y)+0.5-center); d < inner {
				t.Fatalf("the countdown changed (%d,%d), %.1f from the middle, inside its ring", x, y, d)
			}
			changed = true
		}
	}
	if !changed {
		t.Fatal("the countdown drew nothing")
	}
	// Filled half way round from the top: the right of the face is filled, the left only the faint track.
	mid := rimOut - countdownWidth/2
	lift := func(x, y int) int {
		a, b := half.RGBAAt(x, y), off.RGBAAt(x, y)
		return int(a.R) + int(a.G) + int(a.B) - int(b.R) - int(b.G) - int(b.B)
	}
	if filled, track := lift(center+mid, center-20), lift(center-mid, center-20); filled <= track || track <= 0 {
		t.Errorf("the right lifted %d, the left %d: want the fill brighter than the track, and a track", filled, track)
	}
	if c := half.RGBAAt(center+mid, center-20); c == colText {
		t.Error("the ring is solid: the photo does not show through it")
	}
	if bytes.Equal(draw(true, 0.25).Pix, half.Pix) {
		t.Error("the ring does not grow with the time gone")
	}

	if dir := os.Getenv("SPOT_PREVIEW"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "saver-spot-countdown.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, draw(true, 0.4)); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
