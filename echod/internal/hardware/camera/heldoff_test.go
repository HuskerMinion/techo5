//go:build !dot && !panel

package camera

import (
	"context"
	"image"
	"strings"
	"testing"
	"time"
)

// closeCover runs a test with the lens cover closed.
func closeCover(t *testing.T) {
	t.Helper()
	was := coverClosed
	coverClosed = func() bool { return true }
	t.Cleanup(func() { coverClosed = was })
}

// A closed cover keeps the camera off: Acquire refuses, and the run loop's reason names it.
func TestLensCoverHoldsTheCameraOff(t *testing.T) {
	c := offDevice(t, func(stop, stopped chan struct{}) { close(stopped) })
	closeCover(t)
	if why := heldOff(); why != "lens cover closed" {
		t.Errorf("heldOff() = %q, want the lens cover", why)
	}
	if _, err := c.Acquire(); err == nil || !strings.Contains(err.Error(), "lens cover") {
		t.Errorf("Acquire with the cover closed: %v, want a refusal that names the cover", err)
	}
}

// A slow viewer ends on the newest frame: the black one a stopping sensor sends last.
func TestListenLatestKeepsTheNewest(t *testing.T) {
	c := &Camera{}
	ch := make(chan *Frame, 1)
	cancel := c.ListenLatest(ch)
	defer cancel()
	live := &Frame{raw: []byte{1}}
	black := &Frame{rgba: image.NewRGBA(image.Rect(0, 0, 4, 4))}
	c.emit(live)
	c.emit(black) // the viewer has not taken live yet
	got := <-ch
	if got != black || !got.Blank() {
		t.Fatalf("viewer got %+v, want the black frame", got)
	}
	if live.Blank() || (&Frame{}).Blank() {
		t.Error("only the stopped sensor's black frame is blank")
	}
}

// A snapshot that catches the black frame says the camera is off rather than hand it out.
func TestSnapshotRefusesTheBlackFrame(t *testing.T) {
	var c *Camera
	c = offDevice(t, func(stop, stopped chan struct{}) {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				c.emit(&Frame{At: time.Now(), rgba: image.NewRGBA(image.Rect(0, 0, 4, 4))})
			}
		}
	})
	f, err := c.Snapshot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the camera is off") {
		t.Fatalf("snapshot of a black frame: %v, %v; want an error", f, err)
	}
}
