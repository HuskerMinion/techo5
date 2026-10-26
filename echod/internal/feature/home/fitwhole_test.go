package home

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Rect, image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

// A see-through PNG comes out opaque either way: crossfade blends bytes and counts on it.
func TestPhotosComeOutOpaque(t *testing.T) {
	src := solid(300, 600, color.RGBA{}) // all see-through
	for name, img := range map[string]*image.RGBA{"whole": fitWhole(src, 960, 480), "filled": cropToFill(src, 960, 480)} {
		for i := 3; i < len(img.Pix); i += 4 {
			if img.Pix[i] != 255 {
				t.Errorf("%s: pixel %d has alpha %d", name, i/4, img.Pix[i])
				break
			}
		}
	}
}

// A tall photo on a wide screen shows whole, in the middle, with its own colors darkened down the
// sides, not black bars and not cropped.
func TestWholePhotoKeepsATallPhotoWhole(t *testing.T) {
	if roundPanel {
		t.Skip("a round screen fits the circle instead: fitwhole_spot_test.go")
	}
	red := color.RGBA{R: 200, G: 40, B: 20, A: 255}
	src := solid(300, 600, red)
	draw.Draw(src, image.Rect(0, 0, 300, 1), image.NewUniform(color.RGBA{G: 255, A: 255}), image.Point{}, draw.Src)     // the top row, which a crop to fill would cut
	draw.Draw(src, image.Rect(0, 599, 300, 600), image.NewUniform(color.RGBA{B: 255, A: 255}), image.Point{}, draw.Src) // and the bottom

	got := fitWhole(src, 1000, 500)
	if got.Bounds() != image.Rect(0, 0, 1000, 500) {
		t.Fatalf("got %v, want the screen's size", got.Bounds())
	}
	if c := got.RGBAAt(500, 250); c != red {
		t.Errorf("the middle is %v, want the photo's %v", c, red)
	}
	if c := got.RGBAAt(500, 0); c.G < 100 {
		t.Errorf("the photo's top row is gone: %v", c)
	}
	if c := got.RGBAAt(500, 499); c.B < 100 {
		t.Errorf("the photo's bottom row is gone: %v", c)
	}
	for _, x := range []int{0, 50, 949, 999} {
		c := got.RGBAAt(x, 250)
		want := byte(int(red.R) * wholeShade / 256)
		if c.A != 255 || c.R < want-4 || c.R > want+4 || c.G >= c.R {
			t.Errorf("side at x=%d is %v, want the photo's red darkened to about %d", x, c, want)
		}
	}
}

// A photo already the screen's shape fills it, as it would cropped.
func TestWholePhotoOfTheScreensShapeFillsIt(t *testing.T) {
	if roundPanel {
		t.Skip("a round screen fits the circle instead: fitwhole_spot_test.go")
	}
	src := solid(2000, 1000, color.RGBA{R: 10, G: 120, B: 230, A: 255})
	got, crop := fitWhole(src, 1000, 500), cropToFill(src, 1000, 500)
	for i := range got.Pix {
		if got.Pix[i] != crop.Pix[i] {
			t.Fatalf("byte %d differs from the plain crop", i)
		}
	}
}

// A screen whose size is not a whole number of blur blocks, a wide photo on a square screen (the
// Spot's), and an empty picture all come through without a panic, at the screen's size.
func TestWholePhotoOddSizes(t *testing.T) {
	for _, tc := range []struct{ sw, sh, w, h int }{
		{1600, 900, 480, 480},
		{37, 1001, 1279, 799},
		{0, 0, 480, 480},
		{300, 600, 1, 1},
		{300, 600, 10, 5},
	} {
		got := fitWhole(solid(tc.sw, tc.sh, color.RGBA{R: 90, G: 90, B: 90, A: 255}), tc.w, tc.h)
		if got.Bounds() != image.Rect(0, 0, tc.w, tc.h) {
			t.Errorf("%dx%d on %dx%d: got %v", tc.sw, tc.sh, tc.w, tc.h, got.Bounds())
		}
	}
}

// A camera's picture on its view, as Camera picture says: fitted whole, filling the panel with the rest
// cut off, or at its own size where that is smaller than the panel.
func TestCameraFrameFollowsCameraPicture(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	p := image.Pt(1280, 800)
	screenSize.Store(&p)
	t.Cleanup(func() { screenSize.Store(nil) })
	src := solid(640, 480, color.RGBA{200, 0, 0, 255})
	if b := cameraFrame(src).Bounds(); b.Dx() != 1066 || b.Dy() != 800 {
		t.Errorf("whole: %v, want 1066x800", b.Size())
	}
	if err := config.Set().Screen().CameraFit("fill"); err != nil {
		t.Fatal(err)
	}
	if b := cameraFrame(src).Bounds(); b.Dx() != 1280 || b.Dy() != 800 {
		t.Errorf("fill: %v, want the panel's 1280x800", b.Size())
	}
	if err := config.Set().Screen().CameraFit("native"); err != nil {
		t.Fatal(err)
	}
	if b := cameraFrame(src).Bounds(); b.Dx() != 640 || b.Dy() != 480 {
		t.Errorf("native: %v, want the camera's 640x480", b.Size())
	}
	if b := cameraFrame(solid(1920, 1080, color.RGBA{0, 0, 200, 255})).Bounds(); b.Dx() != 1280 || b.Dy() > 800 {
		t.Errorf("native, a picture larger than the panel: %v, want it fitted", b.Size())
	}
}
