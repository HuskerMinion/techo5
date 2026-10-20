//go:build hwdevice && !dot

package camstream

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

// On a Show 8 or Show 5 only: the board's H.264 stream as the pump makes it, pictures a second over
// HW_SECONDS (10) after the camera settles. HW_MAXSHUT caps the exposure, in lines, to see the
// sensor's shortest frame in any light (and, its gain then high, the color smoothed on the VPU: nr.go;
// HW_NR_OFF leaves that out); HW_GAIN pins the gain (4, 6: x) at a 30-a-second frame instead.
func TestDeviceH264Rate(t *testing.T) {
	if !hasEncoder() {
		t.Skip("no encoder")
	}
	if v, err := strconv.Atoi(os.Getenv("HW_MAXSHUT")); err == nil && v > 0 {
		defer camera.CapExposureForTest(v)()
	}
	if v, err := strconv.ParseFloat(os.Getenv("HW_GAIN"), 64); err == nil && v > 0 {
		defer camera.PinGainForTest(int(v*64 + 0.5))()
	}
	if os.Getenv("HW_NR_OFF") != "" {
		defer func(f func() bool) { nrAvailable = f }(nrAvailable)
		nrAvailable = func() bool { return false }
	}
	secs := 10
	if v, err := strconv.Atoi(os.Getenv("HW_SECONDS")); err == nil && v > 0 {
		secs = v
	}
	release, err := camera.Get().Acquire()
	if err != nil {
		t.Fatal(err)
	}
	f := build()
	ch := make(chan sendable, h264Queue)
	f.subs[ch] = struct{}{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { f.pumpH264(stop, release); close(done) }()
	defer func() { close(stop); <-done }()
	var n, bytes, keys int
	var nr0, nrT0 int64
	settle := time.After(3 * time.Second)
	for waiting := true; waiting; {
		select {
		case <-ch:
		case <-settle:
			waiting = false
		}
	}
	nr0, nrT0 = nrPictures.Load(), nrTime.Load()
	end := time.After(time.Duration(secs) * time.Second)
	for counting := true; counting; {
		select {
		case p := <-ch:
			h := p.(h264Picture)
			n, bytes = n+1, bytes+len(h.au)
			if h.key {
				keys++
			}
		case <-end:
			counting = false
		}
	}
	st := Stream()
	if nr := nrPictures.Load() - nr0; nr > 0 {
		t.Logf("%d pictures had their color smoothed (dim light), %.1f ms each", nr,
			float64(nrTime.Load()-nrT0)/float64(nr)/1e6)
	} else {
		t.Logf("no picture had its color smoothed")
	}
	t.Logf("%dx%d: %d pictures in %ds (%.1f a second), %d keyframes, %.1f Mbit/s",
		st.W, st.H, n, secs, float64(n)/float64(secs), keys, float64(bytes)*8/float64(secs)/1e6)
}
