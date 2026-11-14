//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The clock over photos, on every panel: beside each small line a snowy picture is brought down to
// where the words read, beside the time it is only shaded, and a page with no photo never takes the
// two passes. With SHOW_PREVIEW set, each is
// written there to look at.
func TestTheClockReadsOverAPhoto(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	sky := home.Weather{Condition: "partlycloudy", Temp: "72°"}
	dir := os.Getenv("SHOW_PREVIEW")
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}} {
		plain := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		plain.draw(scene{now: at, phase: "idle", weather: sky})
		if plain.over.spare != nil {
			t.Errorf("%s: a page with no photo took the two passes", panel.name)
		}
		for _, kind := range []string{"snow", "night", "sky"} {
			photo := testPhoto(kind, panel.wide, panel.high)
			scenes := map[string]scene{
				"photo-" + kind:       {now: at, phase: "idle", weather: sky, slideshow: photo},
				"photo-saver-" + kind: {now: at, phase: "idle", slideshowScreensaver: photo},
			}
			for name, s := range scenes {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				r := newRenderer(img)
				r.draw(s)
				if name == "photo-snow" {
					// Just left of the date, inside its patch.
					date := at.Format("Monday, January 2")
					base := panel.high/2 + r.s(60) + r.s(70)
					c := img.RGBAAt((panel.wide-r.width(r.small, date))/2-r.s(4), base-r.s(12))
					if l := luma(c.R, c.G, c.B); l > scrimTarget+12 {
						t.Errorf("%s: beside the date a snowy photo is still %.0f bright", panel.name, l)
					}
					// Left of the time, clear of its edge: shaded, and nowhere near as dark as that.
					row := panel.high/2 + r.s(60) - r.s(80)
					c = img.RGBAAt(firstInk(img, row, 1)-r.s(20), row)
					far := img.RGBAAt(r.s(8), panel.high-r.s(8))
					if l := luma(c.R, c.G, c.B); l < scrimTarget+8 || l > luma(far.R, far.G, far.B)-20 {
						t.Errorf("%s: beside the time the photo is %.0f, in the corner %.0f", panel.name, l, luma(far.R, far.G, far.B))
					}
				}
				if dir == "" {
					continue
				}
				f, err := os.Create(filepath.Join(dir, name+panel.name+".png"))
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
}

// firstInk is where the first pixel in the text color is along a row, going across from the left
// (step 1) or back from the right (step -1), or -1.
func firstInk(img *image.RGBA, y, step int) int {
	x := img.Rect.Min.X
	if step < 0 {
		x = img.Rect.Max.X - 1
	}
	for ; x >= img.Rect.Min.X && x < img.Rect.Max.X; x += step {
		if img.RGBAAt(x, y) == cream {
			return x
		}
	}
	return -1
}

// The time can be read over a white photo, which is as bad as a picture gets: the wash leaves it
// 1.7:1 under the text color and the same brightness as the accent the AM/PM used to be drawn in.
// The time's patch takes the picture round it to 2.5:1 or better, its edge is 7:1 where it meets
// the digits, and the AM/PM is in the text color, on the same patch.
func TestTheTimeReadsOverAWhitePhoto(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}, {"skylight", skylightWide, skylightHigh}} {
		photo := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		draw.Draw(photo, photo.Rect, image.White, image.Point{}, draw.Src)
		for name, s := range map[string]scene{
			"background":  {now: at, phase: "idle", slideshow: photo},
			"screensaver": {now: at, phase: "idle", slideshowScreensaver: photo},
		} {
			img := image.NewRGBA(photo.Rect)
			r := newRenderer(img)
			r.draw(s)
			base := panel.high/2 + r.s(60)
			row := base - r.s(80)
			edge := firstInk(img, row, 1)
			if edge < r.s(40) {
				t.Fatalf("%s %s: the time was not found", panel.name, name)
			}
			bare := colorContrast(cream, img.RGBAAt(r.s(8), panel.high-r.s(8)))
			shade := colorContrast(cream, img.RGBAAt(edge-r.s(20), row))
			halo := colorContrast(cream, img.RGBAAt(edge-2, row))
			t.Logf("%s %s: the text color is %.2f:1 on the washed photo, %.2f:1 on the time's patch, %.2f:1 on its edge",
				panel.name, name, bare, shade, halo)
			if shade < 2.5 {
				t.Errorf("%s %s: the time is %.2f:1 on its patch, want 2.5", panel.name, name, shade)
			}
			if halo < 7 {
				t.Errorf("%s %s: the time is %.2f:1 on its edge, want 7", panel.name, name, halo)
			}
			// The AM/PM, found from the right along the middle of its letters.
			arow := base - capHeight(r.ampm)/2
			ax := firstInk(img, arow, -1)
			if ax < panel.wide/2 {
				t.Fatalf("%s %s: no AM/PM in the text color", panel.name, name)
			}
			for x := ax; x > panel.wide/2; x-- {
				if img.RGBAAt(x, arow) == amber {
					t.Fatalf("%s %s: the AM/PM is in the accent over a photo", panel.name, name)
				}
			}
			if on := colorContrast(cream, img.RGBAAt(ax+r.s(12), arow)); on < 2.5 {
				t.Errorf("%s %s: the AM/PM is %.2f:1 on its patch, want 2.5", panel.name, name, on)
			}
		}
	}
}
