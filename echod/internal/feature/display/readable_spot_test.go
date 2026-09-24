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

// The round clock over photos: between the time and the date a snowy picture is brought down to
// where the words read. With SPOT_PREVIEW set, each is written there to look at.
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
			newRoundRenderer(img).draw(s)
			if name == "photo-snow" {
				// Between the time and the date, inside their patches.
				c := img.RGBAAt(center, 262)
				if l := luma(c.R, c.G, c.B); l > scrimTarget+4 {
					t.Errorf("between the time and the date a snowy photo is still %.0f bright", l)
				}
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
