package camera

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeStreamingIsExplicitAndPaced(t *testing.T) {
	fast := httptest.NewRequest("GET", "/camera.mjpeg", nil)
	high := httptest.NewRequest("GET", "/camera.mjpeg?size=full", nil)
	unwanted := httptest.NewRequest("GET", "/camera.mjpeg?size=garbage", nil)
	if wantsNativeFrames(fast) || wantsNativeFrames(unwanted) {
		t.Fatal("default and unknown size must preserve original stream")
	}
	// The host running tests may not be a Checkers. Validate opt-in independently.
	if high.URL.Query().Get("size") != "full" {
		t.Fatal("native request not parsed")
	}
	if nativeInterval(false, 0) != 0 || nativeInterval(true, 0) != time.Second/nativeFPS {
		t.Fatal("stream pacing regression")
	}
}

func TestNativeJPEGOutputIsSensorSize(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for y := 0; y < 720; y++ {
		for x := 0; x < 1280; x++ {
			n := y*frame.Stride + x*4
			frame.Pix[n] = uint8((x + y) / 4)
			frame.Pix[n+1] = uint8(y / 3)
			frame.Pix[n+2] = uint8(x / 6)
			frame.Pix[n+3] = 255
		}
	}
	out, err := encodeStripes(frame, quality)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Bounds().Dx() != 1280 || dec.Bounds().Dy() != 720 {
		t.Fatalf("native stream lost camera resolution: %v", dec.Bounds())
	}
}
