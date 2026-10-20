package display

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"math/rand"
	"testing"
)

// washOver gives the bytes draw.Draw gives for a uniform color drawn Over.
func TestWashOverMatchesDraw(t *testing.T) {
	rnd := rand.New(rand.NewSource(5))
	for n := 0; n < 200; n++ {
		src := image.NewRGBA(image.Rect(0, 0, 37, 23))
		rnd.Read(src.Pix)
		a := uint8(rnd.Intn(256))
		c := color.RGBA{uint8(rnd.Intn(int(a) + 1)), uint8(rnd.Intn(int(a) + 1)), uint8(rnd.Intn(int(a) + 1)), a}
		r := image.Rect(rnd.Intn(40)-3, rnd.Intn(26)-3, rnd.Intn(40), rnd.Intn(26))
		want := image.NewRGBA(src.Rect)
		copy(want.Pix, src.Pix)
		draw.Draw(want, r, image.NewUniform(c), image.Point{}, draw.Over)
		got := image.NewRGBA(src.Rect)
		copy(got.Pix, src.Pix)
		washOver(got, r, c)
		if !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("color %v over %v differs", c, r)
		}
	}
}

func BenchmarkWashPanel(b *testing.B) {
	dst := image.NewRGBA(image.Rect(0, 0, 960, 480))
	c := color.RGBA{10, 20, 30, 120}
	for i := 0; i < b.N; i++ {
		washOver(dst, dst.Rect, c)
	}
}

func BenchmarkWashPanelDraw(b *testing.B) {
	dst := image.NewRGBA(image.Rect(0, 0, 960, 480))
	c := image.NewUniform(color.RGBA{10, 20, 30, 120})
	for i := 0; i < b.N; i++ {
		draw.Draw(dst, dst.Rect, c, image.Point{}, draw.Over)
	}
}
