//go:build linux && hwdevice

package camstream

import (
	"bytes"
	"context"
	"image/png"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/v4l2m2m"
)

var pngEncode = png.Encode

// On a Show 8 only: a camera frame at HW_RAW (2112x1568 packed RAW10) made into 1456x1088 H.264 60 times
// (with HW_CROP set, its middle 1920x1088), written to HW_OUT.
func TestDeviceEncode(t *testing.T) {
	path, outPath := os.Getenv("HW_RAW"), os.Getenv("HW_OUT")
	if path == "" || outPath == "" {
		t.Skip("HW_RAW, HW_OUT not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{W: 1456, H: 1088, FPS: 30, Bitrate: 8000000, GOP: 30}
	if os.Getenv("HW_CROP") != "" { // the middle 1920x1088, pixel for pixel
		cfg = Config{W: 1920, H: 1088, FPS: 30, Bitrate: 10000000, GOP: 30, Crop: true}
	}
	s, err := New(2112, 1568, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	began := time.Now()
	keys, bytes := 0, 0
	for i := 0; i < 60; i++ {
		if i == 45 {
			s.ForceKey()
		}
		b, key, err := s.Encode(raw, 2640, Tone{Black: 64, Red: 711, Blue: 720, Gain: 2048})
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if key {
			keys++
		}
		bytes += len(b)
		out.Write(b)
	}
	el := time.Since(began)
	t.Logf("60 frames in %s (%.1f ms a frame), %d keyframes, %d bytes", el.Round(time.Millisecond),
		float64(el.Milliseconds())/60, keys, bytes)
}

// On a Show 8 only: one live frame, its raw bytes, the cores' picture and the ISP's NV12 (planes as
// the encoder reads them) written under HW_DUMP, to compare the two developments' color.
func TestDeviceColor(t *testing.T) {
	dir := os.Getenv("HW_DUMP")
	if dir == "" {
		t.Skip("HW_DUMP not set")
	}
	release, err := camera.Get().Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	time.Sleep(3 * time.Second) // exposure settles
	f, err := camera.Get().Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, w, h, bpl, tn, ok := f.ISPRaw()
	if !ok {
		t.Fatal("an empty frame")
	}
	t.Logf("raw %dx%d bpl %d tone %+v", w, h, bpl, tn)
	os.WriteFile(dir+"/frame.raw", raw[:bpl*h], 0o644)
	var png bytes.Buffer
	if err := pngEncode(&png, f.Image()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(dir+"/cpu.png", png.Bytes(), 0o644)
	s, err := New(w, h, Config{W: 1456, H: 1088, FPS: 30, Bitrate: 8000000, GOP: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.Encode(raw, bpl, Tone(tn)); err != nil {
		t.Fatal(err)
	}
	y, uv := s.ispOut.Planes[0], s.ispOut.Planes[1]
	os.WriteFile(dir+"/dip.nv12", append(append([]byte(nil), y[:1456*1088]...), uv[:1456*544]...), 0o644)
}

// On a Show 8 only: the frame HW_DUMP/frame.raw developed by the ISP in each Bayer order, to
// HW_DUMP/<fourcc>.nv12.
func TestDeviceOrders(t *testing.T) {
	dir := os.Getenv("HW_DUMP")
	if dir == "" {
		t.Skip("HW_DUMP not set")
	}
	raw, err := os.ReadFile(dir + "/frame.raw")
	if err != nil {
		t.Fatal(err)
	}
	defer func(f uint32) { rawFourcc = f }(rawFourcc)
	for _, fc := range []string{"pBAA", "pGAA", "pgAA", "pRAA"} {
		rawFourcc = v4l2m2m.Fourcc(fc)
		cfg := Config{W: 1456, H: 1088, FPS: 30, Bitrate: 8000000, GOP: 30}
		if os.Getenv("HW_CROP") != "" { // the middle 1920x1088, pixel for pixel
			cfg = Config{W: 1920, H: 1088, FPS: 30, Bitrate: 10000000, GOP: 30, Crop: true}
		}
		s, err := New(2112, 1568, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Encode(raw, 2640, Tone{Black: 72, Red: 686, Blue: 660, Gain: 128}); err != nil {
			t.Fatal(fc, err)
		}
		y, uv := s.ispOut.Planes[0], s.ispOut.Planes[1]
		os.WriteFile(dir+"/"+fc+".nv12", append(append([]byte(nil), y[:1456*1088]...), uv[:1456*544]...), 0o644)
		s.Close()
	}
}

// On a Show 8 only: HW_DUMP/frame.raw through the ISP with the color matrix a saturation of
// HW_SAT (BT.601 luma kept), to HW_DUMP/sat.nv12.
func TestDeviceSaturation(t *testing.T) {
	dir := os.Getenv("HW_DUMP")
	sat, _ := strconv.ParseFloat(os.Getenv("HW_SAT"), 64)
	if dir == "" || sat == 0 {
		t.Skip("HW_DUMP, HW_SAT not set")
	}
	raw, err := os.ReadFile(dir + "/frame.raw")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{W: 1456, H: 1088, FPS: 30, Bitrate: 8000000, GOP: 30}
	if os.Getenv("HW_CROP") != "" { // the middle 1920x1088, pixel for pixel
		cfg = Config{W: 1920, H: 1088, FPS: 30, Bitrate: 10000000, GOP: 30, Crop: true}
	}
	s, err := New(2112, 1568, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l := [3]float64{0.299, 0.587, 0.114}
	m := make([]int32, 9)
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			v := (1 - sat) * l[c]
			if r == c {
				v += sat
			}
			m[3*r+c] = int32(math.Round(v * 512))
		}
	}
	if err := s.isp.SetCtrlArray(0x00982a01, m); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Encode(raw, 2640, Tone{Black: 72, Red: 686, Blue: 660, Gain: 128}); err != nil {
		t.Fatal(err)
	}
	y, uv := s.ispOut.Planes[0], s.ispOut.Planes[1]
	os.WriteFile(dir+"/sat.nv12", append(append([]byte(nil), y[:1456*1088]...), uv[:1456*544]...), 0o644)
	t.Logf("matrix %v", m)
}
