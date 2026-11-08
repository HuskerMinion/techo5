package camera

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

func TestNativeTurboJPEGValidAndFullSize(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for y := 0; y < 720; y++ {
		for x := 0; x < 1280; x++ {
			at := y*im.Stride + x*4
			im.Pix[at] = uint8(x/7 + y/10)
			im.Pix[at+1] = uint8(y / 3)
			im.Pix[at+2] = uint8(x / 6)
			im.Pix[at+3] = 255
		}
	}
	out, err := encodeNativeTurbo(im, 85)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Bounds().Dx() != 1280 || dec.Bounds().Dy() != 720 {
		t.Fatalf("JPEG output is not full sensor size: %v", dec.Bounds())
	}
	for _, y := range []int{0, 189, 190, 191, 192, 383, 384, 575, 576, 719} {
		rr, gg, bb, _ := dec.At(640, y).RGBA()
		if rr == 0 && gg == 0 && bb == 0 {
			t.Fatalf("blank restart band at %d", y)
		}
	}
}

func BenchmarkNativeTurboJPEG720p(b *testing.B) {
	im := picture(1280, 720)
	b.ReportAllocs()
	for range b.N {
		if _, err := encodeNativeTurbo(im, 85); err != nil {
			b.Fatal(err)
		}
	}
}
