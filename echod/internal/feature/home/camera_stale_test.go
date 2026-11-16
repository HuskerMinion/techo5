package home

import (
	"errors"
	"image"
	"testing"
	"time"
)

// A frame that fails after one has come leaves the last picture up with why beside it, which is how the
// screen knows the picture is no longer live; the next frame clears it.
func TestAFailedFrameKeepsTheLastPictureAndSaysSo(t *testing.T) {
	f := &Feature{}
	f.cam = CameraView{Entity: "camera.porch", Until: time.Now().Add(time.Minute), gen: 1}
	first := image.NewRGBA(image.Rect(0, 0, 4, 4))
	f.showFrame("camera.porch", 1, first, nil)
	if got, _ := f.Camera(); got.Frame != first || got.Error != "" {
		t.Fatalf("the first frame: frame %v, error %q", got.Frame != nil, got.Error)
	}
	f.showFrame("camera.porch", 1, nil, errors.New("the camera did not answer"))
	if got, _ := f.Camera(); got.Frame != first || got.Error == "" {
		t.Errorf("a failed frame: the last picture kept %v, error %q", got.Frame == first, got.Error)
	}
	next := image.NewRGBA(image.Rect(0, 0, 4, 4))
	f.showFrame("camera.porch", 1, next, nil)
	if got, _ := f.Camera(); got.Frame != next || got.Error != "" {
		t.Errorf("the next frame: new picture %v, error %q", got.Frame == next, got.Error)
	}
}
