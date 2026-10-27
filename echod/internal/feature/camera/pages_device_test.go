//go:build linux && hwdevice && !dot && !spot && !panel

package camera

import (
	"bytes"
	"image"
	"image/jpeg"
	"syscall"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

// cpuTime is the process's CPU time so far, user and system.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// On a device only: a streamed page's picture made from the camera's own frames, as the pages make it
// here (full size where fullPages, by the encoder where hwEncode), with the time and the CPU time each
// took, against the half-size picture every other board sends.
func TestDevicePageEncode(t *testing.T) {
	release, err := camera.Get().Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	frames := make(chan *camera.Frame, 1)
	defer camera.Get().ListenLatest(frames)()
	time.Sleep(2 * time.Second) // auto-exposure settles
	const n = 20
	var full, half struct {
		wall, cpu time.Duration
		bytes     int
	}
	var size image.Point
	for range n {
		fr := <-frames
		size = fr.Full().Bounds().Size()
		fr = <-frames // a frame not developed yet: the development is part of the cost
		start, c := time.Now(), cpuTime()
		b, err := encodeFrame(fr)
		if err != nil {
			t.Fatal(err)
		}
		full.wall, full.cpu, full.bytes = full.wall+time.Since(start), full.cpu+cpuTime()-c, full.bytes+len(b)
		start, c = time.Now(), cpuTime()
		h, err := encodeHalf(fr)
		if err != nil {
			t.Fatal(err)
		}
		half.wall, half.cpu, half.bytes = half.wall+time.Since(start), half.cpu+cpuTime()-c, half.bytes+len(h)
		if img, err := jpeg.Decode(bytes.NewReader(b)); err != nil || img.Bounds() != fr.Full().Bounds() {
			t.Fatalf("the full-size picture decodes as %v, %v", img.Bounds(), err)
		}
	}
	t.Logf("hwEncode=%v: full size %v: %v a picture, %v CPU, %d bytes", hwEncode, size, full.wall/n, full.cpu/n, full.bytes/n)
	t.Logf("half size: %v a picture, %v CPU, %d bytes", half.wall/n, half.cpu/n, half.bytes/n)
}
