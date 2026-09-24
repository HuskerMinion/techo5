package home

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Music Assistant's picture: laid out for the panel like a station's cover, cleared by an empty one, and
// never shown over a newer one that arrived while it was being decoded.
func TestRemoteArt(t *testing.T) {
	t.Cleanup(func() { RemoteArt(nil, nil) })
	yes := func() bool { return true }

	RemoteArt(testJPEG(t, 600, 600), yes)
	art, thumb := remoteArt()
	if art == nil || thumb == nil {
		t.Fatal("a picture was sent but none is shown")
	}
	if art.Bounds().Dx() != artW || art.Bounds().Dy() != artH {
		t.Errorf("picture is %v, want the panel's %dx%d", art.Bounds(), artW, artH)
	}
	if thumb.Bounds().Dx() != thumbSide {
		t.Errorf("thumb is %v, want %d square", thumb.Bounds(), thumbSide)
	}

	// A picture overtaken while it was decoded leaves the newer one standing.
	RemoteArt(testJPEG(t, 300, 300), func() bool { return false })
	if now, _ := remoteArt(); now != art {
		t.Error("an overtaken picture replaced the current one")
	}

	// A picture that cannot be read is no picture: the page draws its stand-in.
	RemoteArt([]byte("not a picture"), yes)
	if now, _ := remoteArt(); now != nil {
		t.Error("an unreadable picture left the old one showing")
	}

	RemoteArt(testJPEG(t, 600, 600), yes)
	RemoteArt(nil, yes)
	if now, _ := remoteArt(); now != nil {
		t.Error("an empty picture did not clear it")
	}
}
