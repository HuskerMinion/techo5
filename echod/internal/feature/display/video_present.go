//go:build !dot

package display

import (
	"image"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/screen"
)

// A video frame is panel pixels (PresentFrame), unless a hardware decoder hands its frames over in a
// form of its own (video.Frame.YUV): its presenter then puts them on the panel and into screenshots,
// registered from the decoder's own file. With none registered such a frame is drawn as pixels.
var (
	presentYUV func(dev *screen.Device, fr *video.Frame, over image.Rectangle) error
	shotYUV    func(img *image.RGBA, fr *video.Frame)
)

// presentFrame puts a video frame on the panel, through the presenter of its form.
func presentFrame(dev *screen.Device, fr *video.Frame, over image.Rectangle) error {
	if fr.YUV != nil && presentYUV != nil {
		return presentYUV(dev, fr, over)
	}
	return dev.PresentFrame(fr.Pix, over)
}
