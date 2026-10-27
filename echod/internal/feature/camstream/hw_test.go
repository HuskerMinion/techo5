package camstream

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

// Every board but the 2nd gen Show 8 with its JPEG encoder streams as before: at most maxFPS pictures
// a second, each encoded on a core at quality.
func TestOtherBoardsStreamAsBefore(t *testing.T) {
	if hwStream {
		t.Fatal("the JPEG encoder is used on a board that is not the 2nd gen Show 8 on its mainline kernel")
	}
	if streamFPS() != maxFPS || maxFPS != 10 {
		t.Fatalf("%d pictures a second, want %d", streamFPS(), maxFPS)
	}
	assertCoreEncode(t)
}

// Where the encoder is used but fails a picture (here, a host that has none), the picture is encoded on
// a core instead, the same as on every other board.
func TestAFailedHardwareEncodeGoesToACore(t *testing.T) {
	was := hwStream
	hwStream = true
	t.Cleanup(func() { hwStream = was })
	if streamFPS() != hwFPS {
		t.Fatalf("%d pictures a second with the encoder, want %d", streamFPS(), hwFPS)
	}
	assertCoreEncode(t)
}

func assertCoreEncode(t *testing.T) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1056, 784))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	got, err := encode(img)
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := jpeg.Encode(&want, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Error("not the single encode on a core at the stream's quality")
	}
}
