//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// inkBounds is the box of every pixel paint lays down on a clear canvas the panel's size.
func inkBounds(w, h int, paint func(r *renderer)) image.Rectangle {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	paint(newRenderer(img))
	var b image.Rectangle
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if img.Pix[img.PixOffset(x, y)+3] != 0 {
				b = b.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return b
}

// textBox is where a line of words in face stands, drawn with its baseline at y from x.
func textBox(r *renderer, face font.Face, s string, x, y int) image.Rectangle {
	b, _ := font.BoundString(face, s)
	return image.Rect(x+b.Min.X.Floor(), y+b.Min.Y.Floor(), x+b.Max.X.Ceil(), y+b.Max.Y.Ceil())
}

// Today's icon keeps clear of the words under it and of the reading beside it, whatever the weather,
// on every panel: a storm's bolt hung to within a pixel or two of "Thunderstorms", and its rain
// reached for the reading.
func TestTheWeatherIconClearsItsWords(t *testing.T) {
	conds := []string{"sunny", "clear-night", "partlycloudy", home.PartlyCloudyNight, "cloudy", "rainy", "pouring",
		"lightning", "lightning-rainy", "snowy", "snowy-rainy", "fog", "hail", "windy", "exceptional"}
	for _, p := range []struct {
		name string
		w, h int
	}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}, {"skylight", skylightWide, skylightHigh}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, p.w, p.h)))
		at := r.weatherLayout(r.s(90))
		reading := textBox(r, r.big, "100°", at.reading.X, at.reading.Y)
		gap := r.s(8)
		for _, c := range conds {
			icon := inkBounds(p.w, p.h, func(q *renderer) { q.weatherIcon(c, at.icon.X, at.icon.Y, at.iconSize) })
			words := textBox(r, r.body, conditionWords(c), r.margin, at.cond)
			if icon.Max.Y+gap > words.Min.Y {
				t.Errorf("%s %s: the icon reaches y=%d, the words start at %d", p.name, c, icon.Max.Y, words.Min.Y)
			}
			if icon.Max.X+gap > reading.Min.X {
				t.Errorf("%s %s: the icon reaches x=%d, the reading starts at %d", p.name, c, icon.Max.X, reading.Min.X)
			}
		}
	}
}

// A panel taller than the Show 5's shape spreads the weather page over its height: the forecast boxes
// reach as far down it as they do on a Show 5, while everything still clears the Radar button.
func TestTheWeatherPageUsesATallPanel(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { config.Use(filepath.Join(t.TempDir(), "state.json")) })
	for _, size := range textSteps {
		_ = config.Set().Screen().TextSize(size)
		weatherPageAt(t, size)
	}
}

func weatherPageAt(t *testing.T, size int) {
	t.Helper()
	for _, p := range []struct {
		name string
		w, h int
	}{{"show5", showWide, showHigh}, {"show8", show8Wide, show8High}, {"skylight", skylightWide, skylightHigh}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, p.w, p.h)))
		at := r.weatherLayout(r.s(90))
		btn := r.weatherButton()
		if rain := textBox(r, r.small, weatherRain(100), r.margin, at.rain); rain.Max.Y+r.s(8) > btn.Min.Y && rain.Max.X > btn.Min.X {
			t.Errorf("%d%% on the %s: the rain line ends at %d, over the button at %d", size, p.name, rain.Max.Y, btn.Min.Y)
		}
		if at.boxFoot > btn.Min.Y-r.s(8) {
			t.Errorf("%d%% on the %s: the forecast boxes end at %d, on the button at %d", size, p.name, at.boxFoot, btn.Min.Y)
		}
		// A Show 5 lays its boxes over most of the panel; a taller panel does no worse.
		if float64(at.boxFoot)/float64(p.h) < 0.74 {
			t.Errorf("%d%% on the %s: the forecast boxes end at %d of %d, leaving the foot empty", size, p.name, at.boxFoot, p.h)
		}
	}
}
