package alarm

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// The Dot's light before an alarm, drawn as the device wears it: a ring of one color, taken from
// sunriseColor at each point of the ramp. With RING_PREVIEW set to a directory it writes a frame per
// step, which is the only way to look at this without standing in a dark room at six in the morning.
//
// It is a picture of the colors, not of the plastic: the ring is where the light comes out and the
// glow around it is what a table under it does with that light.
func TestSunriseRingDraws(t *testing.T) {
	const (
		side   = 420
		steps  = 20
		outer  = 150.0 // the ring's outer edge
		inner  = 118.0 // and its inner one
		glowTo = 205.0 // how far the light reaches across the table
	)

	dir := os.Getenv("RING_PREVIEW")
	for i := 0; i <= steps; i++ {
		p := float64(i) / steps
		c := sunriseColor(p)
		if c.R == 0 && c.G == 0 && c.B == 0 && p > 0 {
			t.Errorf("progress %.2f gives no color at all", p)
		}

		img := image.NewRGBA(image.Rect(0, 0, side, side))
		mid := float64(side) / 2
		for y := range side {
			for x := range side {
				d := math.Hypot(float64(x)+0.5-mid, float64(y)+0.5-mid)

				// The puck: matte dark, a little lighter than the ground so it reads as an object.
				out := color.RGBA{18, 18, 20, 255}
				switch {
				case d <= inner:
					out = color.RGBA{26, 26, 29, 255}
				case d <= outer:
					out = color.RGBA{c.R, c.G, c.B, 255}
				case d <= glowTo:
					// Light falling on the table, fading with distance.
					f := 1 - (d-outer)/(glowTo-outer)
					f = f * f * 0.55
					out = color.RGBA{
						R: uint8(float64(c.R) * f),
						G: uint8(float64(c.G) * f),
						B: uint8(float64(c.B) * f),
						A: 255,
					}
				}
				img.SetRGBA(x, y, out)
			}
		}
		if dir == "" {
			continue
		}
		f, err := os.Create(filepath.Join(dir, "sunrise-"+two(i)+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func two(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
