//go:build linux && hwdevice

package jpegenc

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
	"time"
)

// On a device with the encoder only: pictures of the camera's sizes, each encoded a few times, decoded
// back at their size, with the time each took against Go's encoder on the cores.
func TestDeviceEncode(t *testing.T) {
	if !Available() {
		t.Skip("no mtk-jpeg-enc on this board")
	}
	for _, c := range []struct{ w, h, q int }{{2112, 1568, 90}, {1056, 784, 70}, {4224, 3136, 90}} {
		img := image.NewRGBA(image.Rect(0, 0, c.w, c.h))
		for y := range c.h {
			for x := range c.w {
				i := img.PixOffset(x, y)
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(x), uint8(y), uint8(x^y), 255
			}
		}
		var jpg []byte
		var best time.Duration
		for i := range 5 {
			start := time.Now()
			b, err := Encode(img, c.q)
			if err != nil {
				t.Fatalf("%dx%d: %v", c.w, c.h, err)
			}
			if d := time.Since(start); i == 0 || d < best {
				best = d
			}
			jpg = b
		}
		got, err := jpeg.Decode(bytes.NewReader(jpg))
		if err != nil || got.Bounds().Dx() != c.w || got.Bounds().Dy() != c.h {
			t.Fatalf("%dx%d: decodes as %v, %v", c.w, c.h, got.Bounds(), err)
		}
		start := time.Now()
		var soft bytes.Buffer
		jpeg.Encode(&soft, img, &jpeg.Options{Quality: c.q})
		t.Logf("%dx%d q%d: %v, %d bytes (Go's encoder %v, %d bytes)", c.w, c.h, c.q, best, len(jpg), time.Since(start), soft.Len())
	}
}
