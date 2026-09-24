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

// The clock over photos, on both panels: beside each small line a snowy picture is brought down to
// where the words read, beside the time it is left alone, and a page with no photo never takes the
// two passes. With SHOW_PREVIEW set, each is
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
					// Just left of the date, inside its patch.
					date := at.Format("Monday, January 2")
					base := panel.high/2 + r.s(60) + r.s(70)
					c := img.RGBAAt((panel.wide-r.width(r.small, date))/2-r.s(4), base-r.s(12))
					if l := luma(c.R, c.G, c.B); l > scrimTarget+6 {
						t.Errorf("%s: beside the date a snowy photo is still %.0f bright", panel.name, l)
					}
					// Just left of the time: no patch there.
					hour := clockHM(at)
					x := (panel.wide-r.width(r.clock, hour)-r.s(18)-r.width(r.ampm, clockSuffix(at)))/2 - r.s(4)
					c = img.RGBAAt(x, panel.high/2+r.s(60)-r.s(80))
					if l := luma(c.R, c.G, c.B); l < scrimTarget+30 {
						t.Errorf("%s: beside the time the photo was darkened to %.0f", panel.name, l)
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
