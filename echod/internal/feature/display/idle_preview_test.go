//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// TestIdlePreview writes the clock page with the idle orb and a glance strip, on the Ocean theme, at
// both Show sizes to $ORB_PREVIEW (skipped when unset), and reports what a frame of it costs.
func TestIdlePreview(t *testing.T) {
	dir := os.Getenv("ORB_PREVIEW")
	if dir == "" {
		t.Skip("set ORB_PREVIEW to a directory")
	}
	applyTheme(themes[themeIndex("Ocean")])
	defer applyTheme(themes[0])
	chips := []home.Chip{
		{Icon: "account-multiple", Text: "Guest mode"},
		{Icon: "washing-machine", Text: "Washer done"},
		{Icon: "bell-ring-outline", Text: "6:00 PM: water the plants"},
	}
	idleOrbForced = true
	defer func() { idleOrbForced = false }()
	for _, size := range []image.Point{{960, 480}, {1280, 800}} {
		dst := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
		r := newRenderer(dst)
		s := scene{now: time.Now(), phase: "idle", glance: chips}
		frame := func() { r.draw(s) }
		frame()
		start := time.Now()
		const n = 20
		for i := 0; i < n; i++ {
			s.now = s.now.Add(idleOrbFrame)
			frame()
		}
		t.Logf("%dx%d clock page with the idle orb and three chips: %v a frame", size.X, size.Y, time.Since(start)/n)
		f, err := os.Create(fmt.Sprintf("%s/idle-%dx%d.png", dir, size.X, size.Y))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, dst)
		f.Close()
	}
}
