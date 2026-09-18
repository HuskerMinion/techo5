package home

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// withOrientation is a small JPEG carrying an EXIF orientation, in the byte order given.
func withOrientation(t *testing.T, orientation int, order binary.ByteOrder) []byte {
	var img bytes.Buffer
	if err := jpeg.Encode(&img, image.NewRGBA(image.Rect(0, 0, 4, 2)), nil); err != nil {
		t.Fatal(err)
	}
	tiff := make([]byte, 8+2+12+4)
	if order == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	order.PutUint16(tiff[2:], 42)
	order.PutUint32(tiff[4:], 8)
	order.PutUint16(tiff[8:], 1)       // one entry
	order.PutUint16(tiff[10:], 0x0112) // Orientation
	order.PutUint16(tiff[12:], 3)      // SHORT
	order.PutUint32(tiff[14:], 1)
	order.PutUint16(tiff[18:], uint16(orientation))
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(app1)+2))
	b := img.Bytes()
	return append(append(append([]byte{}, b[:2]...), append(seg, app1...)...), b[2:]...)
}

func TestExifOrientation(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for o := 1; o <= 8; o++ {
			if got := exifOrientation(withOrientation(t, o, order)); got != o {
				t.Errorf("%v orientation %d read as %d", order, o, got)
			}
		}
	}
	var plain bytes.Buffer
	_ = jpeg.Encode(&plain, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil)
	if got := exifOrientation(plain.Bytes()); got != 1 {
		t.Errorf("no EXIF read as %d", got)
	}
	if got := exifOrientation([]byte("\x89PNG\r\n\x1a\n")); got != 1 {
		t.Errorf("a PNG read as %d", got)
	}
}

// A marked corner ends up where each orientation says it should.
func TestUpright(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2)) // wide
	red := color.RGBA{255, 0, 0, 255}
	src.Set(0, 0, red) // stored top left
	for o, want := range map[int]image.Point{
		1: {0, 0}, 2: {2, 0}, 3: {2, 1}, 4: {0, 1}, // same shape
		5: {0, 0}, 6: {1, 0}, 7: {1, 2}, 8: {0, 2}, // turned: tall
	} {
		out := upright(src, o)
		b := out.Bounds()
		if (o >= 5) != (b.Dx() == 2 && b.Dy() == 3) {
			t.Errorf("orientation %d gave %v", o, b)
		}
		if got := out.At(want.X, want.Y); got != (color.Color)(red) {
			r, g, bl, _ := got.RGBA()
			t.Errorf("orientation %d: the stored top left isn't at %v (found %d,%d,%d)", o, want, r>>8, g>>8, bl>>8)
		}
	}
}
