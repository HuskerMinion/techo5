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
// The panel is 960×480, so nothing that large was ever going to be shown. The one tried here is
// 9000×6000, past the photo budget: at 20000×15000 the png package on 32-bit ARM, which the
// devices are, refuses the size itself before the budget is asked, and the test saw its words.
func TestAnAbsurdlyLargeImageIsRefusedBeforeItIsDecoded(t *testing.T) {
	if _, err := decodeWithin(hugePNG(9000, 6000), maxPhotoPixels, "a photo", 0, 0); err == nil {
		t.Fatal("a 54 megapixel photo was decoded")
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
		"map tile":     {4000, 4000, maxTilePixels},
	} {
		if _, err := decodeWithin(hugePNG(tc.w, tc.h), tc.max, name, 0, 0); err == nil {
			t.Errorf("%s: %d×%d was decoded", name, tc.w, tc.h)
		}
	}
}

// A media server's poster or fanart is within the art budget (#516): Plex sends 2000×3000 posters and
// 3840×2160 fanart, and the old four megapixel bound refused both and left Now Playing blank. These
// JPEGs, which is what both are, so they are measured against the JPEG budget.
func TestAPostersSizeIsWithinTheArtBudget(t *testing.T) {
	for _, s := range [][2]int64{{2000, 3000}, {3840, 2160}} {
		if s[0]*s[1] > maxArtPixels {
			t.Errorf("%d×%d is over the %d pixel art budget", s[0], s[1], maxArtPixels)
		}
	}
}

// Art in a format decoded whole keeps the tighter bound: only JPEG decodes at a fraction of its size, and
// a 12 megapixel PNG is 48 MB unpacked.
func TestArtDecodedWholeKeepsTheTighterBound(t *testing.T) {
	if got := artPixels(hugePNG(3840, 2160)); got != maxWholeArtPixels {
		t.Fatalf("a PNG's budget is %d, want %d", got, maxWholeArtPixels)
	}
	if _, err := decodeWithin(hugePNG(3840, 2160), artPixels(hugePNG(3840, 2160)), "cover art", 960, 480); err == nil ||
		!strings.Contains(err.Error(), "pixels") {
		t.Errorf("a 3840×2160 PNG cover: %v, want refused on its size", err)
	}
	if got := artPixels([]byte("not a picture")); got != maxArtPixels {
		t.Errorf("an unreadable header's budget is %d; decodeWithin refuses it anyway", got)
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

	got, err := decodeWithin(b.Bytes(), maxPhotoPixels, "a photo", 0, 0)
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
	if _, err := decodeWithin([]byte("<html>not found</html>"), maxPhotoPixels, "a photo", 0, 0); err == nil {
		t.Error("an HTML page was taken for a photo")
	}
}
