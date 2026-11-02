//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// TestWeatherFxPreview writes frames of the rain on the glass and a storm's strikes, over the weather
// art and over the weather page, into $SHOW_PREVIEW, to look at.
func TestWeatherFxPreview(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW not set")
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	rise, set := at.Add(-7*time.Hour), at.Add(5*time.Hour)
	// Strikes to show: the first few moments in the next minutes where a bolt is near its brightest.
	var flashes []time.Time
	for d := time.Duration(0); d < 10*time.Minute && len(flashes) < 4; d += fxFrame {
		secs := float64(at.Add(d).UnixMilli()) / 1000
		s := int(secs / strikeSlot)
		if !strikes(s) || rnd(s, 103) < 0.3 {
			continue
		}
		if t0 := secs - (float64(s)+rnd(s, 102)*0.6)*strikeSlot; t0 >= 0 && t0 < 0.03 {
			flashes = append(flashes, at.Add(d))
			d += 20 * time.Second
		}
	}
	for _, size := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}} {
		for _, cond := range []struct {
			name, ha string
			fx       skyFx
			k        artCond
		}{{"rain", "rainy", fxRain, artRain}, {"storm", "lightning-rainy", fxStorm, artStorm}} {
			k := artKey{cond.k, artDay, size.X, size.Y, artSeed()}
			arts.mu.Lock()
			arts.land = paintLandscape(k)
			arts.mu.Unlock()
			times := []time.Time{at, at.Add(1500 * time.Millisecond), at.Add(4 * time.Second)}
			if cond.fx == fxStorm {
				times = flashes
			}
			for n, now := range times {
				for _, page := range []string{"art", "page"} {
					img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
					r := newRenderer(img)
					sc := scene{now: now, phase: "idle", weather: home.Weather{Condition: cond.ha, Temp: "54°"}}
					if page == "art" {
						sc.slideshow, sc.artFx = artFrame(now, cond.ha, rise, set, true, artMoon{}, size.X, size.Y), cond.fx
					} else {
						sc.showWeather, sc.sky = true, cond.fx
					}
					r.draw(sc)
					name := filepath.Join(dir, fmt.Sprintf("fx-%s-%s-%dx%d-%d.png", cond.name, page, size.X, size.Y, n))
					f, err := os.Create(name)
					if err != nil {
						t.Fatal(err)
					}
					if err := png.Encode(f, img); err != nil {
						t.Fatal(err)
					}
					f.Close()
					if size.X == show8Wide {
						zoom := image.NewRGBA(image.Rect(0, 0, 4*320, 4*200))
						for y := range 4 * 200 {
							for x := range 4 * 320 {
								zoom.Set(x, y, img.At([]int{size.X / 2, 0, 0, 500}[n]+x/4, []int{size.Y / 3, 300, 300, 100}[n]+y/4))
							}
						}
						f, _ := os.Create(name[:len(name)-4] + "-zoom.png")
						png.Encode(f, zoom)
						f.Close()
					}
				}
			}
		}
	}
}
