//go:build !dot

package display

import (
	"image"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/screen"
)

// A frame in a decoder's own form goes to its presenter; panel pixels, and any frame with no presenter
// registered, are presented as pixels.
func TestVideoFramesGoToTheirPresenter(t *testing.T) {
	saved := presentYUV
	t.Cleanup(func() { presentYUV = saved })
	var presented *video.Frame
	presentYUV = func(_ *screen.Device, fr *video.Frame, _ image.Rectangle) error { presented = fr; return nil }
	fr := &video.Frame{YUV: &video.YUVFrames{W: 2, H: 2}}
	if err := presentFrame(nil, fr, image.Rectangle{}); err != nil || presented != fr {
		t.Fatalf("%v, presented %v", err, presented)
	}
}
