//go:build linux && hwdevice

package facedetect

import (
	"os"
	"testing"
	"time"
)

// On a Show 8 only: a camera frame at HW_RAW (the stream's 2112x1568 packed RAW10) through the ISP and
// the detector.
func TestDeviceDetect(t *testing.T) {
	path := os.Getenv("HW_RAW")
	if path == "" {
		t.Skip("HW_RAW not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(2112, 1568)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := 0; i < 3; i++ {
		began := time.Now()
		f, err := d.Detect(raw, 2640, Tone{Black: 64, Red: 711, Blue: 720, Gain: 2048})
		if err != nil {
			t.Fatal(err)
		}
		w, h := d.Size()
		t.Logf("%dx%d: %d faces in %s: %+v", w, h, len(f), time.Since(began).Round(time.Millisecond), f)
	}
}

func TestDeviceTimes(t *testing.T) {
	path := os.Getenv("HW_RAW")
	if path == "" {
		t.Skip("HW_RAW not set")
	}
	raw, _ := os.ReadFile(path)
	d, err := New(2112, 1568)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := 0; i < 3; i++ {
		a := time.Now()
		copy(d.dip.in, raw[:2640*1568])
		b := time.Now()
		n, err := d.dip.run(2640*1568, bufTypeCaptureMplane)
		c := time.Now()
		copy(d.fd.in, d.dip.out[:n])
		e := time.Now()
		_, err2 := d.fd.run(d.w*d.h*2, bufTypeMetaCapture)
		g := time.Now()
		t.Logf("copy in %s, ISP %s, copy across %s, detector %s (%v %v)", b.Sub(a), c.Sub(b), e.Sub(c), g.Sub(e), err, err2)
	}
}
