//go:build linux && hwdevice && !dot && !spot && !panel

package camstream

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/lib/rtpjpeg"
)

// On a device only: the stream's pictures made from the camera's own frames, as the stream makes them
// here (by the JPEG encoder where hwStream), each one RTP/JPEG can carry, with the time each took.
func TestDeviceStreamPicture(t *testing.T) {
	release, err := camera.Get().Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	frames := make(chan *camera.Frame, 1)
	defer camera.Get().ListenLatest(frames)()
	time.Sleep(2 * time.Second) // auto-exposure settles
	const n = 20
	var took time.Duration
	size := 0
	for range n {
		img := eighths((<-frames).Image())
		start := time.Now()
		b, err := encode(img)
		took += time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		f, err := rtpjpeg.Parse(b)
		if err != nil {
			t.Fatalf("a picture RTP/JPEG cannot carry: %v", err)
		}
		if f.Width != img.Bounds().Dx() || f.Height != img.Bounds().Dy() {
			t.Fatalf("%dx%d sent for a %v picture", f.Width, f.Height, img.Bounds().Size())
		}
		size += len(b)
	}
	t.Logf("hwStream=%v: %v, %v a picture, %d bytes, at most %d a second", hwStream, eighths((<-frames).Image()).Bounds().Size(),
		took/n, size/n, streamFPS())
}
