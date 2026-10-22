//go:build linux && hwdevice

package camera

import (
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"
)

// On a device only: the sensor's frame rate over HW_SECONDS (8 by default) once auto-exposure has
// settled, with the exposure it settled on (its debug log); HW_MAXSHUT caps the exposure, in lines, to see
// the rate of a frame that short.
func TestDeviceRate(t *testing.T) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	secs := 8 * time.Second
	if d, err := time.ParseDuration(os.Getenv("HW_SECONDS") + "s"); err == nil && d > 0 {
		secs = d
	}
	if v, err := strconv.Atoi(os.Getenv("HW_MAXSHUT")); err == nil && v > 0 { // the rate a short frame gives
		defer func(f, m int) { aeFrame, aeMaxShut = f, m }(aeFrame, aeMaxShut)
		aeFrame, aeMaxShut = min(aeFrame, v), v
	}
	release, err := Get().Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	frames := make(chan time.Time, 256)
	defer Get().Frames.Listen(func(fr *Frame) {
		select {
		case frames <- fr.At:
		default:
		}
	})()
	time.Sleep(3 * time.Second) // auto-exposure settles
	for len(frames) > 0 {
		<-frames
	}
	n, end := 0, time.After(secs)
	for {
		select {
		case <-frames:
			n++
		case <-end:
			t.Logf("%d frames in %s: %.1f a second", n, secs, float64(n)/secs.Seconds())
			return
		}
	}
}
