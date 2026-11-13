//go:build !dot && !spot && !panel

package camera

import (
	"errors"
	"testing"
)

func withCaptures(t *testing.T, cs ...capture) {
	t.Helper()
	saved := captures
	captures = cs
	t.Cleanup(func() { captures = saved })
}

// The first capture whose driver is there opens the camera, and the camera is there with it.
func TestTheFirstCaptureThereOpensTheCamera(t *testing.T) {
	opened := ""
	wantErr := errors.New("opened")
	fake := func(name string, there bool) capture {
		return capture{name: name, probe: func() bool { return there },
			open: func() (*device, error) { opened = name; return nil, wantErr }}
	}
	withCaptures(t, fake("absent", false), fake("there", true), fake("later", true))
	if c := activeCapture(); c == nil || c.name != "there" {
		t.Fatalf("active %+v", c)
	}
	if !Available() {
		t.Fatal("a capture's camera is not there")
	}
	if _, err := open(); err != wantErr || opened != "there" {
		t.Fatalf("open: %v by %q", err, opened)
	}
	withCaptures(t, fake("absent", false))
	if activeCapture() != nil {
		t.Fatal("a capture that is not there is active")
	}
}

// A capture's chain gets the exposure, the frames and the close, as the athena kernel's does.
func TestADeviceWithAChain(t *testing.T) {
	c := &fakeChain{}
	d := &device{v: c, shutter: 100, gain: 64}
	d.setExposure(200, 64, 0)
	d.setExposure(200, 64, 0) // unchanged: not asked again
	d.stream(make(chan struct{}), func([]byte) {})
	d.close()
	if c.exposures != 1 || c.shutter != 200 || !c.streamed || !c.closed {
		t.Fatalf("%+v", c)
	}
}

type fakeChain struct {
	exposures, shutter, gain int
	streamed, closed         bool
}

func (c *fakeChain) exposure(shutter, gain int) {
	c.exposures, c.shutter, c.gain = c.exposures+1, shutter, gain
}
func (c *fakeChain) stream(chan struct{}, func([]byte)) { c.streamed = true }
func (c *fakeChain) close()                             { c.closed = true }
