//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// gradientPair is a made-up pair of photos side by side, as the slideshow puts them up: warm on the
// left, cool on the right, a dark line between.
func gradientPair(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(80 + 120*y/h)
			c := color.RGBA{v, uint8(60 + 60*x/w), 40, 255}
			if x >= w/2 {
				c = color.RGBA{40, uint8(60 + 60*x/w), v, 255}
			}
			if x >= w/2-3 && x < w/2+3 {
				c = color.RGBA{0, 0, 0, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// The day and place of each photo of a pair are written over its own half, at its foot, whatever the
// screensaver's clock; with none, the page is as it was. With SHOW_PREVIEW set, each is written there.
func TestPhotoCaptionsOnTheScreensaver(t *testing.T) {
	at := time.Date(2026, 10, 17, 9, 41, 0, 0, time.Local)
	caps := []home.PhotoCaption{
		{When: time.Date(2019, 6, 15, 14, 30, 0, 0, time.UTC), Place: "Springfield, Ohio", Pair: true},
		{When: time.Date(2020, 1, 2, 8, 0, 0, 0, time.UTC), Place: "Canada", Pair: true, Right: true},
	}
	dir := os.Getenv("SHOW_PREVIEW")
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}} {
		photo := gradientPair(panel.wide, panel.high)
		for _, overlay := range []string{config.SlideshowOverlayOff, config.SlideshowOverlaySmall, ""} {
			draw := func(c []home.PhotoCaption) *image.RGBA {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				newRenderer(img).draw(scene{now: at, phase: "idle", slideshowScreensaver: photo, slideshowOverlay: overlay, slideshowCaptions: c})
				return img
			}
			plain, captioned := draw(nil), draw(caps)
			if same(plain.Pix, captioned.Pix) {
				t.Errorf("overlay %q%s: the captions changed nothing", overlay, panel.name)
			}
			// Written at the foot only: the top half of the page is as it was.
			top := panel.high / 2 * panel.wide * 4
			if !slices.Equal(plain.Pix[:top], captioned.Pix[:top]) {
				t.Errorf("overlay %q%s: the captions reached the top of the page", overlay, panel.name)
			}
			if dir == "" {
				continue
			}
			name := "caption-pair-" + map[string]string{config.SlideshowOverlayOff: "noclock", config.SlideshowOverlaySmall: "small", "": "clock"}[overlay] + panel.name + ".png"
			f, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, captioned); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
