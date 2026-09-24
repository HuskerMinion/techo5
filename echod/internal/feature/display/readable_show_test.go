//go:build !dot && !spot

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

// The clock over photos, on both panels: beside the time a snowy picture is brought down to where
// the words read, and a page with no photo never takes the two passes. With SHOW_PREVIEW set, each is
// written there to look at.
func TestTheClockReadsOverAPhoto(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	sky := home.Weather{Condition: "partlycloudy", Temp: "72°"}
	dir := os.Getenv("SHOW_PREVIEW")
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}} {
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
					// Just left of the time, inside its patch.
					hour := clockHM(at)
					x := (panel.wide-r.width(r.clock, hour)-r.s(18)-r.width(r.ampm, clockSuffix(at)))/2 - r.s(4)
					c := img.RGBAAt(x, panel.high/2+r.s(60)-r.s(30))
					if l := luma(c.R, c.G, c.B); l > scrimTarget+4 {
						t.Errorf("%s: beside the time a snowy photo is still %.0f bright", panel.name, l)
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
