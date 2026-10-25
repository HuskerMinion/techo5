//go:build panel

package home

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// panelScreen has the screen say it is w by h for the test, and forgets it after.
func panelScreen(t *testing.T, w, h int) {
	t.Helper()
	p := image.Pt(w, h)
	screenSize.Store(&p)
	t.Cleanup(func() { screenSize.Store(nil) })
}

// A panel's pictures are the size its screen says, not the Show 5's they start at: a photo, a pair,
// a now-playing cover, the rain map and a camera frame (#207).
func TestPanelPicturesAreTheScreensSize(t *testing.T) {
	if w, h := slideshowSize(); w != 960 || h != 480 {
		t.Fatalf("before the screen says, photos are %dx%d, want the 960x480 they start at", w, h)
	}
	panelScreen(t, 1280, 800)
	want := image.Rect(0, 0, 1280, 800)
	red := color.RGBA{220, 30, 30, 255}

	for _, whole := range []bool{false, true} {
		if b := fitShown([]image.Image{solid(400, 300, red)}, whole).Bounds(); b != want {
			t.Errorf("photo (whole %v) is %v, want %v", whole, b, want)
		}
	}
	if b := fitShown([]image.Image{solid(300, 400, red), solid(300, 400, red)}, false).Bounds(); b != want {
		t.Errorf("pair is %v, want %v", b, want)
	}
	art, _, err := layoutArt(testJPEG(t, 600, 600), false, "a cover")
	if err != nil {
		t.Fatal(err)
	}
	if art.Bounds() != want {
		t.Errorf("cover is %v, want %v", art.Bounds(), want)
	}
	if w, h := radarSize(); w != 1280 || h != 800 {
		t.Errorf("rain map is %dx%d, want 1280x800", w, h)
	}
	if b := fitFrame(solid(1920, 1080, red)).Bounds(); b.Dx() != 1280 || b.Dy() > 800 {
		t.Errorf("camera frame is %v, want 1280 wide within 800", b)
	}
}

// The photo up when the screen says its size is fetched again at it, without a fade between the two
// sizes; the rain map is made again; and the same size said again changes nothing.
func TestPanelScreenSizedRefetchesThePhoto(t *testing.T) {
	m := &moreImmich{}
	f := slideshowFeature(t, m, func(*config.Slideshow) {}, wide1, wide2)
	f.advanceSlideshow()
	if b := f.currentSlideshowPhoto().Bounds(); b != image.Rect(0, 0, 960, 480) {
		t.Fatalf("first photo is %v, want 960x480", b)
	}
	f.radar.fetched, f.radar.view.Frames = time.Now(), []RadarFrame{{Image: image.NewRGBA(image.Rect(0, 0, 960, 480))}}
	t.Cleanup(func() { screenSize.Store(nil) })

	f.screenSized(1280, 800)
	want := image.Rect(0, 0, 1280, 800)
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		img := f.slideshow.image
		f.mu.Unlock()
		if img.Bounds() == want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("photo up is %v, want it fetched again at %v", img.Bounds(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b := f.currentSlideshowPhoto().Bounds(); b != want {
		t.Errorf("mid-fade the photo is %v, want %v", b, want)
	}
	if !f.radar.fetched.IsZero() || len(f.radar.view.Frames) != 0 {
		t.Error("the rain map at the old size was kept")
	}

	f.radar.fetched = time.Now()
	f.screenSized(1280, 800)
	if f.radar.fetched.IsZero() {
		t.Error("the same size said again made the rain map again")
	}
}
