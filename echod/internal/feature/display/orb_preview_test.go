//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"
	"time"
)

// TestOrbPreview writes one picture of each turn phase on a Show 5 and a Show 8 screen to
// $ORB_PREVIEW (skipped when unset) and reports the time a frame takes, for looking at the orb
// without a device.
func TestOrbPreview(t *testing.T) {
	dir := os.Getenv("ORB_PREVIEW")
	if dir == "" {
		t.Skip("set ORB_PREVIEW to a directory")
	}
	for _, size := range []image.Point{{960, 480}, {1280, 800}} {
		dst := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
		r := newRenderer(dst)
		since := time.Now().Add(-1300 * time.Millisecond)
		for _, c := range []struct {
			phase        string
			mic, out     float64
			heard, reply string
		}{
			{"listening", 0.7, 0, "", ""},
			{"thinking", 0, 0, "Turn the kitchen lights down to thirty percent", ""},
			{"replying", 0, 0.45, "Turn the kitchen lights down to thirty percent", "Done, the kitchen is at thirty percent."},
		} {
			s := scene{now: time.Now(), phase: c.phase, since: since, heard: c.heard, reply: c.reply, micLevel: c.mic, outLevel: c.out}
			r.orb(s)
			start := time.Now()
			const n = 20
			for i := 0; i < n; i++ {
				s.now = s.now.Add(orbFrame)
				r.orb(s)
			}
			t.Logf("%dx%d %s: %v a frame", size.X, size.Y, c.phase, time.Since(start)/n)
			f, err := os.Create(fmt.Sprintf("%s/orb-%dx%d-%s.png", dir, size.X, size.Y, c.phase))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, dst); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
