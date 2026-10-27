//go:build linux

package jpegenc

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

// The structures match the kernel's on the architecture built for, which the request numbers carry:
// arm (the images' 32-bit userspace, through the 64-bit kernel's compat layer) and arm64.
func TestLayouts(t *testing.T) {
	word := unsafe.Sizeof(uintptr(0))
	want := map[string][2]uintptr{ // 32-bit, 64-bit
		"format":          {204, 208},
		"pixFormatMplane": {192, 192},
		"requestBuffers":  {20, 20},
		"plane":           {60, 64},
		"buffer":          {68, 88},
		"control":         {8, 8},
	}
	got := map[string]uintptr{
		"format":          unsafe.Sizeof(format{}),
		"pixFormatMplane": unsafe.Sizeof(pixFormatMplane{}),
		"requestBuffers":  unsafe.Sizeof(requestBuffers{}),
		"plane":           unsafe.Sizeof(plane{}),
		"buffer":          unsafe.Sizeof(buffer{}),
		"control":         unsafe.Sizeof(control{}),
	}
	i := 0
	if word == 8 {
		i = 1
	}
	for name, sizes := range want {
		if got[name] != sizes[i] {
			t.Errorf("%s is %d bytes on %s, want %d", name, got[name], runtime.GOARCH, sizes[i])
		}
	}
	offsets := [][2]uintptr{{52, 64}, {44, 56}} // v4l2_buffer.m and .sequence
	if m := unsafe.Offsetof(buffer{}.m); m != offsets[0][i] {
		t.Errorf("v4l2_buffer.m at %d, want %d", m, offsets[0][i])
	}
	if s := unsafe.Offsetof(buffer{}.sequence); s != offsets[1][i] {
		t.Errorf("v4l2_buffer.sequence at %d, want %d", s, offsets[1][i])
	}
	if u := unsafe.Offsetof(format{}.union); u != word {
		t.Errorf("v4l2_format's union at %d, want %d", u, word)
	}
	// VIDIOC_S_FMT and VIDIOC_QBUF as the kernel's headers number them.
	fmts := [2]uintptr{0xc0cc5605, 0xc0d05605}
	qbufs := [2]uintptr{0xc044560f, 0xc058560f}
	if vidiocSFmt != fmts[i] || vidiocQbuf != qbufs[i] {
		t.Errorf("VIDIOC_S_FMT %#x, VIDIOC_QBUF %#x on %s", vidiocSFmt, vidiocQbuf, runtime.GOARCH)
	}
}

// The encoder is found by its name, on whichever node it has, and a board without it has none.
func TestDevicePath(t *testing.T) {
	dir := t.TempDir()
	was := sysfs
	sysfs = dir
	t.Cleanup(func() { sysfs = was })
	for n, name := range map[string]string{"video0": "mtk-vcodec-enc", "video3": "mtk-jpeg-enc", "video5": "athena-ov13855"} {
		os.MkdirAll(filepath.Join(dir, n), 0o755)
		os.WriteFile(filepath.Join(dir, n, "name"), []byte(name+"\n"), 0o644)
	}
	if got := devicePath(); got != "/dev/video3" {
		t.Errorf("found %q, want /dev/video3", got)
	}
	os.Remove(filepath.Join(dir, "video3", "name"))
	if got := devicePath(); got != "" {
		t.Errorf("found %q on a board without the encoder", got)
	}
}

func TestFits(t *testing.T) {
	for _, c := range []struct {
		w, h int
		ok   bool
	}{{2112, 1568, true}, {1056, 784, true}, {4224, 3136, true}, {1600, 1200, true}, {1000, 600, false}, {1056, 780, false}, {0, 8, false}} {
		if got := Fits(image.Rect(0, 0, c.w, c.h)); got != c.ok {
			t.Errorf("Fits(%dx%d) = %v", c.w, c.h, got)
		}
	}
}

// What comes back is taken from the plane's data offset to the bytes used, without the zeros the
// encoder pads it with, and must be a whole JPEG.
func TestJPEGOf(t *testing.T) {
	buf := []byte{0, 0, 0xFF, 0xD8, 0x00, 0xFF, 0xD9, 0, 0, 0, 7}
	got, err := jpegOf(buf, 10, 2)
	if err != nil || string(got) != "\xFF\xD8\x00\xFF\xD9" {
		t.Errorf("jpegOf = %x, %v", got, err)
	}
	for _, c := range [][2]uint32{{2, 2}, {12, 0}, {7, 0}, {11, 2}, {6, 2}} {
		if _, err := jpegOf(buf, c[0], c[1]); err == nil {
			t.Errorf("jpegOf(%d at %d) took it", c[0], c[1])
		}
	}
}

// YUYV of known colors: white, black, and the primaries, in full-range BT.601.
func TestToYUYV(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 1))
	copy(img.Pix, []byte{
		255, 255, 255, 255, 255, 255, 255, 255,
		0, 0, 0, 255, 0, 0, 0, 255,
		255, 0, 0, 255, 255, 0, 0, 255,
		0, 0, 255, 255, 0, 0, 255, 255,
	})
	out := make([]byte, 16)
	toYUYV(img, out)
	want := []byte{
		255, 128, 255, 128,
		0, 128, 0, 128,
		76, 85, 76, 255,
		29, 255, 29, 107,
	}
	for i := range want {
		if d := int(out[i]) - int(want[i]); d < -1 || d > 1 {
			t.Fatalf("YUYV %v, want %v", out, want)
		}
	}
}

// A picture split in bands across the cores converts the same as row by row.
func TestToYUYVBands(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 37))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 13)
	}
	got := make([]byte, 64*37*2)
	toYUYV(img, got)
	for y := range 37 {
		row := img.SubImage(image.Rect(0, y, 64, y+1)).(*image.RGBA)
		one := make([]byte, 64*2)
		toYUYV(row, one)
		if string(one) != string(got[y*128:(y+1)*128]) {
			t.Fatalf("row %d differs", y)
		}
	}
}

// A failed picture leaves the encoder alone for backOff: the pictures meanwhile are refused at once,
// without trying the device, and the first after it tries the device again.
func TestBackOffAfterAFailure(t *testing.T) {
	findOnce.Do(func() {})
	wasPath, wasNow, wasAttempt := path, now, attempt
	t.Cleanup(func() { path, now, attempt = wasPath, wasNow, wasAttempt; failed.set(time.Time{}) })
	path = "/dev/video-test"
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return clock }
	tries, fail := 0, true
	attempt = func(w, h, q int, fill func([]byte)) ([]byte, error) {
		tries++
		if fail {
			return nil, errors.New("no jpeg within 2s")
		}
		return []byte{0xFF, 0xD8, 0xFF, 0xD9}, nil
	}
	if _, err := EncodeYUYV(64, 8, 80, func([]byte) {}); err == nil || tries != 1 {
		t.Fatalf("the failing picture: %v after %d tries", err, tries)
	}
	for _, after := range []time.Duration{time.Second, backOff / 2, backOff - time.Millisecond} {
		clock = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).Add(after)
		if _, err := EncodeYUYV(64, 8, 80, func([]byte) {}); !errors.Is(err, ErrBackingOff) || tries != 1 {
			t.Fatalf("%v after the failure: %v, %d tries; want backing off with the device left alone", after, err, tries)
		}
	}
	// Past the back-off the device is tried again: failing, it backs off afresh from then.
	clock = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).Add(backOff)
	if _, err := EncodeYUYV(64, 8, 80, func([]byte) {}); err == nil || errors.Is(err, ErrBackingOff) || tries != 2 {
		t.Fatalf("after the back-off: %v, %d tries; want the device tried again", err, tries)
	}
	clock = clock.Add(backOff)
	fail = false
	if b, err := EncodeYUYV(64, 8, 80, func([]byte) {}); err != nil || len(b) != 4 || tries != 3 {
		t.Fatalf("the encoder working again: %v, %d tries", err, tries)
	}
	// A size the encoder doesn't take is not its failure, and starts no back-off.
	if _, err := EncodeYUYV(60, 8, 80, func([]byte) {}); err == nil || errors.Is(err, ErrBackingOff) {
		t.Fatalf("a size the encoder doesn't take: %v", err)
	}
	if _, err := EncodeYUYV(64, 8, 80, func([]byte) {}); err != nil || tries != 4 {
		t.Fatalf("after a size refused: %v, %d tries", err, tries)
	}
}
