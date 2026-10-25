//go:build spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A cue turn keeps the face that was up and lights the rim in the turn's color; the same face with
// no turn has the plain track.
func TestCueRimOverTheFace(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	frame := func(s roundScene) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		newRoundRenderer(img).draw(s)
		return img
	}
	onRim := func(img *image.RGBA) (r, g, b uint8) {
		c := img.RGBAAt(center, center-(rimIn+rimOut)/2)
		return c.R, c.G, c.B
	}
	plain := frame(roundScene{now: at, phase: "idle"})
	cued := frame(roundScene{now: at, phase: "idle", cue: "replying"})
	if r, g, b := onRim(cued); r != colReplying.R || g != colReplying.G || b != colReplying.B {
		t.Errorf("rim while replying is %d,%d,%d, want %v", r, g, b, colReplying)
	}
	if r, g, b := onRim(plain); r == colReplying.R && g == colReplying.G && b == colReplying.B {
		t.Errorf("rim with no turn is the replying color")
	}
	// Inside the rim the face is the idle one.
	differ := 0
	for y := 0; y < side; y += 5 {
		for x := 0; x < side; x += 5 {
			if dx, dy := x-center, y-center; dx*dx+dy*dy > (rimIn-6)*(rimIn-6) {
				continue // the rim, and what is past it
			}
			if plain.RGBAAt(x, y) != cued.RGBAAt(x, y) {
				differ++
			}
		}
	}
	if differ != 0 {
		t.Errorf("%d sampled pixels of the face changed under the cue", differ)
	}
}

// With SPOT_CUE_PREVIEW set, the cue over each face it goes over is written there to look at.
func TestCueSpotPreviews(t *testing.T) {
	dir := os.Getenv("SPOT_CUE_PREVIEW")
	if dir == "" {
		t.Skip("set SPOT_CUE_PREVIEW")
	}
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	reply := "Tomorrow will be sunny, with a high of 74 and a low of 51, and a light wind from the south in the afternoon."
	faces := map[string]roundScene{
		"clock":   {},
		"photos":  {slideshowScreensaver: testPhoto("snow", side, side)},
		"slides":  {slideshow: testPhoto("sky", side, side)},
		"sunrise": {sunrise: 0.6, sunriseFace: true},
		"night":   {redClock: true},
		"onebig":  {},
		"setup":   {setupAsking: true},
	}
	for name, s := range faces {
		for _, cue := range []string{"listening", "thinking", "replying"} {
			s.now, s.phase, s.cue, s.heard, s.reply = at, "idle", cue, "", ""
			suffix := "-" + cue
			if cue == "replying" {
				s.heard, s.reply, suffix = "What's the weather tomorrow?", reply, suffix+"-captions"
				if name == "onebig" {
					s.reply = "Supercalifragilisticexpialidocious-supercalifragilistic-and-more"
				}
			}
			img := image.NewRGBA(image.Rect(0, 0, side, side))
			newRoundRenderer(img).draw(s)
			f, err := os.Create(filepath.Join(dir, "spot-cue-"+name+suffix+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}

// The rim is lit over the faces that paint the whole circle, the photos and the light before an
// alarm, as over the clock: it is drawn after them.
func TestCueRimOverPhotos(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	for name, s := range map[string]roundScene{
		"the screensaver":  {slideshowScreensaver: testPhoto("snow", side, side)},
		"the photo behind": {slideshow: testPhoto("sky", side, side)},
		"the light before": {sunrise: 0.6, sunriseFace: true},
	} {
		s.now, s.phase, s.cue = at, "idle", "replying"
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		newRoundRenderer(img).draw(s)
		if c := img.RGBAAt(center, center-(rimIn+rimOut)/2); c.R != colReplying.R || c.G != colReplying.G || c.B != colReplying.B {
			t.Errorf("%s: rim while replying is %v, want %v", name, c, colReplying)
		}
	}
}

// No captions over a face that waits on an answer: the words change nothing there and leave nothing
// to tap; over the clock they are drawn, and can be tapped away.
func TestCueSpotCaptionsOnlyOverPassiveFaces(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	frame := func(s roundScene, words bool) (*image.RGBA, image.Rectangle) {
		s.now, s.phase, s.cue = at, "idle", "replying"
		if words {
			s.reply = "Tomorrow will be sunny."
		}
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		r := newRoundRenderer(img)
		r.draw(s)
		r.zmu.Lock()
		defer r.zmu.Unlock()
		return img, r.captionsAt
	}
	for name, s := range map[string]roundScene{
		"the setup page asking": {setupAsking: true},
		"the PIN pad":           {pin: pinView{open: true}},
		"the settings":          {sheetOpen: true},
		"an announcement":       {showAnnouncement: true},
	} {
		plain, _ := frame(s, false)
		capd, b := frame(s, true)
		if !samePixelsSpot(plain, capd) || !b.Empty() {
			t.Errorf("%s: captions over it", name)
		}
	}
	if _, b := frame(roundScene{}, true); b.Empty() {
		t.Error("no captions over the clock")
	}
}
