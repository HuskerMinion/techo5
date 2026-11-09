//go:build linux && arm && cgo && !dot && !spot

package camera

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

// Exercises the real libjpeg-turbo C path on ARM; the generic turbo_test.go
// tests the portable fallback on other architectures.
func TestNativeTurboARMJPEGColorAndRestartBoundaries(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 160, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 160; x++ {
			p := canvas.PixOffset(x, y)
			canvas.Pix[p] = uint8(40 + x/2)
			canvas.Pix[p+1] = uint8(20 + y*2)
			canvas.Pix[p+2] = uint8(180 - x/2)
			canvas.Pix[p+3] = 255
		}
	}
	for _, tc := range []struct {
		name string
		rect image.Rectangle
	}{
		{"full-buffer", image.Rect(0, 0, 128, 64)},
		{"cropped-stride", image.Rect(16, 8, 144, 72)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := canvas.SubImage(tc.rect).(*image.RGBA)
			encoded, err := encodeNativeTurbo(src, 95)
			if err != nil {
				t.Fatalf("native libjpeg-turbo encode: %v", err)
			}
			decoded, err := jpeg.Decode(bytes.NewReader(encoded))
			if err != nil {
				t.Fatalf("native JPEG round trip: %v", err)
			}
			if decoded.Bounds().Dx() != 128 || decoded.Bounds().Dy() != 64 {
				t.Fatalf("unexpected decoded dimensions: %v", decoded.Bounds())
			}
			// Sample both sides of every 16-scanline band boundary as
			// well as interior pixels. In particular R and B must not swap.
			for _, y := range []int{8, 15, 16, 24, 31, 32, 40, 47, 48, 56} {
				for _, x := range []int{16, 64, 112} {
					want := src.RGBAAt(tc.rect.Min.X+x, tc.rect.Min.Y+y)
					r, g, b, _ := decoded.At(x, y).RGBA()
					got := [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
					expected := [3]int{int(want.R), int(want.G), int(want.B)}
					for channel := range got {
						delta := got[channel] - expected[channel]
						if delta < 0 {
							delta = -delta
						}
						if delta > 24 {
							t.Errorf("pixel (%d,%d) channel %d: got %d want %d (delta %d)", x, y, channel, got[channel], expected[channel], delta)
						}
					}
				}
			}
		})
	}
}
