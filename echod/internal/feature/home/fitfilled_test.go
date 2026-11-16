package home

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

// With Show whole photo off, only a photo whose orientation is the screen's is cropped to fill it (#581).
func TestOnlyPhotosOfTheScreensShapeFill(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sw, sh, w, h int
		fills        bool
	}{
		{"landscape on a wide screen", 4000, 3000, 1280, 800, true},
		{"16:9 on a wide screen", 1920, 1080, 1280, 800, true},
		{"portrait on a wide screen", 3000, 4000, 1280, 800, false},
		{"a phone's tall photo on a wide screen", 1080, 1920, 960, 480, false},
		{"square on a wide screen", 3000, 3000, 1280, 800, false},
		{"portrait on a tall screen", 3000, 4000, 800, 1280, true},
		{"landscape on a tall screen", 4000, 3000, 800, 1280, false},
		{"4:3 on the round screen", 4000, 3000, 480, 480, true},
		{"3:4 on the round screen", 3000, 4000, 480, 480, true},
		{"square on the round screen", 3000, 3000, 480, 480, true},
		{"16:9 on the round screen", 1920, 1080, 480, 480, false},
		{"9:16 on the round screen", 1080, 1920, 480, 480, false},
		{"nothing known", 0, 0, 1280, 800, true},
	} {
		if got := shapeFills(tc.sw, tc.sh, tc.w, tc.h); got != tc.fills {
			t.Errorf("%s: fills is %v, want %v", tc.name, got, tc.fills)
		}
	}
}

// A portrait photo on a wide screen is shown whole with Show whole photo off, its top row and bottom row
// still there, and a landscape one is cropped to fill as it was.
func TestPortraitPhotoIsNotCroppedOnAWideScreen(t *testing.T) {
	if roundPanel {
		t.Skip("a round screen is square: its own rule is in TestOnlyPhotosOfTheScreensShapeFill")
	}
	p := image.Pt(1000, 500)
	screenSize.Store(&p)
	t.Cleanup(func() { screenSize.Store(nil) })

	red := color.RGBA{R: 200, G: 40, B: 20, A: 255}
	tall := solid(300, 600, red)
	draw.Draw(tall, image.Rect(0, 0, 300, 1), image.NewUniform(color.RGBA{G: 255, A: 255}), image.Point{}, draw.Src)     // the top row, which a crop to fill would cut
	draw.Draw(tall, image.Rect(0, 599, 300, 600), image.NewUniform(color.RGBA{B: 255, A: 255}), image.Point{}, draw.Src) // and the bottom
	got := fitShown([]image.Image{tall}, false)
	if c := got.RGBAAt(500, 0); c.G < 100 {
		t.Errorf("the portrait photo's top row is cut: %v", c)
	}
	if c := got.RGBAAt(500, 499); c.B < 100 {
		t.Errorf("the portrait photo's bottom row is cut: %v", c)
	}
	whole := fitShown([]image.Image{tall}, true)
	for i := range got.Pix {
		if got.Pix[i] != whole.Pix[i] {
			t.Fatalf("byte %d: off, a portrait photo is not the same as whole", i)
		}
	}

	wide := solid(2000, 1000, color.RGBA{R: 10, G: 120, B: 230, A: 255})
	got, crop := fitShown([]image.Image{wide}, false), cropToFill(wide, 1000, 500)
	for i := range got.Pix {
		if got.Pix[i] != crop.Pix[i] {
			t.Fatalf("byte %d: a landscape photo is not the plain crop", i)
		}
	}
}

// Photo pairs are as they were: each tall photo of a pair fills its half, whole photo off.
func TestAPairStillFillsItsHalves(t *testing.T) {
	if roundPanel {
		t.Skip("a round screen has no pairs")
	}
	p := image.Pt(1000, 500)
	screenSize.Store(&p)
	t.Cleanup(func() { screenSize.Store(nil) })
	a, b := solid(300, 600, color.RGBA{R: 200, A: 255}), solid(300, 600, color.RGBA{G: 200, A: 255})
	got := fitShown([]image.Image{a, b}, false)
	half := (1000 - pairGap) / 2
	left, right := cropToFill(a, half, 500), cropToFill(b, half, 500)
	for y := 0; y < 500; y += 50 {
		for x := 0; x < half; x += 50 {
			if got.RGBAAt(x, y) != left.RGBAAt(x, y) || got.RGBAAt(1000-half+x, y) != right.RGBAAt(x, y) {
				t.Fatalf("the pair at %d,%d is not the two photos each filling its half", x, y)
			}
		}
	}
}
