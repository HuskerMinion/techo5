package camera

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

// fakeFrame is a frame's two developments: the half-size picture and the full-size one.
type fakeFrame struct{ half, full *image.RGBA }

func (f fakeFrame) Image() *image.RGBA { return f.half }
func (f fakeFrame) Full() *image.RGBA  { return f.full }

func picture(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	return img
}

// Every board but the 2nd gen Show 5 on a mainline kernel keeps the camera pages as they were: the
// stream is the half-size picture and the still the full-size one, each a single encode at quality 85.
func TestOtherBoardsKeepHalfSizePagesAt85(t *testing.T) {
	was := fullPages
	fullPages = false
	t.Cleanup(func() { fullPages = was })
	if quality != 85 {
		t.Fatalf("quality %d, want 85", quality)
	}
	f := fakeFrame{half: picture(800, 600), full: picture(1600, 1200)}
	for _, c := range []struct {
		name string
		enc  func() ([]byte, error)
		img  *image.RGBA
	}{
		{"stream", func() ([]byte, error) { return encodeHalf(f) }, f.half},
		{"still", func() ([]byte, error) { return encodeFull(f) }, f.full},
	} {
		got, err := c.enc()
		if err != nil {
			t.Fatal(err)
		}
		var want bytes.Buffer
		if err := jpeg.Encode(&want, c.img, &jpeg.Options{Quality: 85}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want.Bytes()) {
			t.Errorf("%s: not the single encode at 85 of the %v picture", c.name, c.img.Bounds().Size())
		}
	}
}
