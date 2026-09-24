//go:build spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The round clock over photos: beside each small line a snowy picture is brought down to where the
// words read. With SPOT_PREVIEW set, each is written there to look at.
func TestTheRoundClockReadsOverAPhoto(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	sky := home.Weather{Condition: "partlycloudy", Temp: "72°"}
	dir := os.Getenv("SPOT_PREVIEW")
	plain := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
	plain.draw(roundScene{now: at, phase: "idle", weather: sky})
	if plain.over.spare != nil {
		t.Error("a face with no photo took the two passes")
	}
	for _, kind := range []string{"snow", "night", "sky"} {
		photo := testPhoto(kind, side, side)
		scenes := map[string]roundScene{
			"photo-" + kind:       {now: at, phase: "idle", weather: sky, slideshow: photo},
			"photo-saver-" + kind: {now: at, phase: "idle", slideshowScreensaver: photo},
		}
		for name, s := range scenes {
			img := image.NewRGBA(image.Rect(0, 0, side, side))
			r := newRoundRenderer(img)
			r.draw(s)
			if name == "photo-snow" {
				besideSmallLines(t, "spot", img, r.over.boxes, r.s(scrimTallest))
			}
			if dir == "" {
				continue
			}
			f, err := os.Create(filepath.Join(dir, name+".png"))
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

// besideSmallLines checks the words pass one found: none taller than a patch is for, some of them,
// and just left of each the picture brought down to where they read.
func besideSmallLines(t *testing.T, name string, img *image.RGBA, boxes []image.Rectangle, tallest int) {
	t.Helper()
	if len(boxes) == 0 {
		t.Errorf("%s: no small lines were found", name)
	}
	for _, b := range boxes {
		if b.Dy() > tallest {
			t.Errorf("%s: a line %d tall got a patch", name, b.Dy())
		}
		c := img.RGBAAt(b.Min.X-2, (b.Min.Y+b.Max.Y)/2)
		if l := luma(c.R, c.G, c.B); l > scrimTarget+12 {
			t.Errorf("%s: beside the words at %v a snowy photo is still %.0f bright", name, b, l)
		}
	}
}
