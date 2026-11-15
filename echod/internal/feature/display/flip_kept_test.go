//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// A card face kept between frames (flipKept) and laid over the screen looks as the face drawn
// straight onto it does, its rounded corners showing what is behind them, wherever the card is.
func TestFlipKeptFaceIsTheFaceDrawn(t *testing.T) {
	ground := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	for y := range showHigh {
		for x := range showWide {
			ground.SetRGBA(x, y, color.RGBA{uint8(x * 255 / showWide), uint8(y * 255 / showHigh), 90, 255})
		}
	}
	r := newRenderer(image.NewRGBA(ground.Rect))
	r.ink = r.dayInk()
	for _, at := range []image.Point{{100, 80}, {137, 91}} { // the second is the drift's: the same face, kept
		card := image.Rect(at.X, at.Y, at.X+180, at.Y+380)
		for _, text := range []string{"7", "O", "PM", ""} {
			fresh, kept := image.NewRGBA(ground.Rect), image.NewRGBA(ground.Rect)
			copy(fresh.Pix, ground.Pix)
			copy(kept.Pix, ground.Pix)
			r.dst = fresh
			r.flipFace(card, text, 12)
			face := r.flipFaceKept(card, text, 12)
			draw.Draw(kept, card, face, face.Rect.Min, draw.Over)
			for i := range fresh.Pix {
				if d := int(fresh.Pix[i]) - int(kept.Pix[i]); d > 2 || d < -2 {
					x, y := (i/4)%showWide, (i/4)/showWide
					t.Fatalf("card at %v showing %q: at (%d,%d) the kept face is %d off the face drawn", at, text, x, y, d)
				}
			}
		}
	}
	if len(r.flipPics) != 4 {
		t.Errorf("%d pictures kept for four faces at one size", len(r.flipPics))
	}
}

// A frame of the Flip clock style while two cards flip, drawn thirty times a second; with the faces
// kept it should cost little more than one standing still.
func BenchmarkFlipFrame(b *testing.B) {
	for _, bc := range []struct {
		name    string
		turning bool
	}{{"turning", true}, {"settled", false}} {
		b.Run(bc.name, func(b *testing.B) {
			config.Use(filepath.Join(b.TempDir(), "state.json"))
			_ = config.Set().Screen().ClockStyle(styleFlip)
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
			at := time.Date(2026, 9, 16, 14, 7, 59, 900e6, time.Local)
			sc := scene{now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}}
			r.draw(sc)
			sc.now = at.Add(200 * time.Millisecond) // 2:08: two cards flip
			r.draw(sc)
			i := 0
			for b.Loop() {
				if bc.turning {
					sc.now = at.Add(200*time.Millisecond + time.Duration(i%16)*flipFrame)
				} else {
					sc.now = at.Add(2*time.Second + time.Duration(i)*time.Millisecond)
				}
				r.draw(sc)
				i++
			}
		})
	}
}
