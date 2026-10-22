package camera

import (
	"bytes"
	"image"
	"image/jpeg"
	"math/rand"
	"testing"
)

// The joined strips decode to the same pixels a single encode gives, at the sizes the cameras stream
// and shoot, a picture not a whole number of MCUs tall included.
func TestStripesDecodeLikeOneEncode(t *testing.T) {
	for _, size := range []image.Point{{1600, 1200}, {2112, 1568}, {800, 600}, {640, 360}, {100, 37}} {
		img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
		r := rand.New(rand.NewSource(int64(size.X)))
		// Smooth regions with noise on top, so the coefficients are neither all zero nor all noise.
		for y := 0; y < size.Y; y++ {
			for x := 0; x < size.X; x++ {
				j := y*img.Stride + x*4
				img.Pix[j] = uint8(x*255/size.X) + uint8(r.Intn(16))
				img.Pix[j+1] = uint8(y*255/size.Y) + uint8(r.Intn(16))
				img.Pix[j+2] = uint8((x+y)%256) + uint8(r.Intn(16))
				img.Pix[j+3] = 255
			}
		}
		joined, err := encodeStripes(img, quality)
		if err != nil {
			t.Fatalf("%v: %v", size, err)
		}
		whole, err := encodeWhole(img, quality)
		if err != nil {
			t.Fatal(err)
		}
		a, err := jpeg.Decode(bytes.NewReader(joined))
		if err != nil {
			t.Fatalf("%v: the joined strips do not decode: %v", size, err)
		}
		b, _ := jpeg.Decode(bytes.NewReader(whole))
		if a.Bounds() != b.Bounds() {
			t.Fatalf("%v: decoded at %v, want %v", size, a.Bounds(), b.Bounds())
		}
		ya, yb := a.(*image.YCbCr), b.(*image.YCbCr)
		if !bytes.Equal(ya.Y, yb.Y) || !bytes.Equal(ya.Cb, yb.Cb) || !bytes.Equal(ya.Cr, yb.Cr) {
			t.Fatalf("%v: the joined strips decode to different pixels", size)
		}
	}
}
