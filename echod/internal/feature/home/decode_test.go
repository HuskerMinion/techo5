package home

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// hugePNG is a PNG that is nothing but a header, declaring a picture of w by h. Written by hand
// because the point is a file whose header lies about what follows: a real one of this size is the
// allocation the code under test exists to refuse.
func hugePNG(w, h uint32) []byte {
	var ihdr bytes.Buffer
	ihdr.WriteString("IHDR")
	_ = binary.Write(&ihdr, binary.BigEndian, w)
	_ = binary.Write(&ihdr, binary.BigEndian, h)
	ihdr.Write([]byte{8, 2, 0, 0, 0}) // 8-bit truecolor, no interlace

	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'})
	_ = binary.Write(&b, binary.BigEndian, uint32(ihdr.Len()-4))
	b.Write(ihdr.Bytes())
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(ihdr.Bytes()))
	return b.Bytes()
}

// A picture is measured before it is unpacked: an 8 MB JPEG can hold 20000×15000 pixels, and
// decoding that asks for the better part of a gigabyte on a device that has one for everything.
// The panel is 960×480, so nothing that large was ever going to be shown.
func TestAnAbsurdlyLargeImageIsRefusedBeforeItIsDecoded(t *testing.T) {
	if _, err := decodeWithin(hugePNG(20000, 15000), maxPhotoPixels, "a photo"); err == nil {
		t.Fatal("a 300 megapixel photo was decoded")
	} else if !strings.Contains(err.Error(), "pixels") {
		t.Errorf("%v, want something about how many pixels it declared", err)
	}

	// The tighter budgets hold as well: a camera frame and a map tile are small by nature, and
	// neither comes from anywhere this device controls.
	for name, tc := range map[string]struct {
		w, h uint32
		max  int64
	}{
		"camera frame": {8000, 6000, maxFramePixels},
		"map tile":     {4000, 4000, maxArtPixels},
	} {
		if _, err := decodeWithin(hugePNG(tc.w, tc.h), tc.max, name); err == nil {
			t.Errorf("%s: %d×%d was decoded", name, tc.w, tc.h)
		}
	}
}

// And a picture the screen could actually show still comes through, whole.
func TestAPictureThePanelCanUseIsDecoded(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, slideshowW, slideshowH))
	src.Set(1, 1, color.RGBA{R: 200, G: 100, B: 50, A: 255})

	var b bytes.Buffer
	if err := png.Encode(&b, src); err != nil {
		t.Fatal(err)
	}

	got, err := decodeWithin(b.Bytes(), maxPhotoPixels, "a photo")
	if err != nil {
		t.Fatal(err)
	}
	if bounds := got.Bounds(); bounds.Dx() != slideshowW || bounds.Dy() != slideshowH {
		t.Errorf("decoded %v, want %d×%d", bounds, slideshowW, slideshowH)
	}
	if r, g, bl, _ := got.At(1, 1).RGBA(); r>>8 != 200 || g>>8 != 100 || bl>>8 != 50 {
		t.Errorf("the pixel came back as %d,%d,%d", r>>8, g>>8, bl>>8)
	}
}

// A header that is not a picture at all is a refusal too, not a panic: the body came off the network
// and may be an error page, a redirect somebody forgot to follow, or nothing.
func TestSomethingThatIsNotAPictureIsRefused(t *testing.T) {
	if _, err := decodeWithin([]byte("<html>not found</html>"), maxPhotoPixels, "a photo"); err == nil {
		t.Error("an HTML page was taken for a photo")
	}
}
