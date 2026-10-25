//go:build mockup && spot

package display

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// Mockups of each skin on the Spot, in a few clock styles with a timer running, as the panel would be
// sent them: drawn, then through the skin's filter (MOCK_OUT).
func TestMockSpotSkins(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	previewFont(t)
	out := os.Getenv("MOCK_OUT")
	at := time.Date(2026, 9, 16, 14, 7, 38, 0, time.Local)
	var tiles []mockTile
	for _, sk := range skins {
		if err := config.Set().Screen().Skin(sk.value); err != nil {
			t.Fatal(err)
		}
		for _, style := range []string{styleClassic, styleAnalog} {
			if err := config.Set().Screen().ClockStyle(style); err != nil {
				t.Fatal(err)
			}
			applySkin(nil, side, side)
			img := image.NewRGBA(image.Rect(0, 0, side, side))
			s := roundScene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"},
				timers: []timer.Countdown{{Name: "pasta", Left: 272 * time.Second, Total: 600 * time.Second, Active: true}}}
			newRoundRenderer(img).draw(s)
			if f := skinFilterFor(side, side); f != nil {
				for y := 0; y < side; y++ {
					f(img.Pix[y*img.Stride:y*img.Stride+side*4], y)
				}
			}
			tiles = append(tiles, mockTile{sk.label + " / " + clockStyles[clockStyleIndex()].label, img})
		}
	}
	mockSheet(t, out, "spot-skins-sheet", tiles, 4)
}
