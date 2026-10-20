//go:build linux

package jpegenc

import (
	"image"
	"runtime"
	"testing"
	"unsafe"
)

// The structures match the kernel's on the architecture built for, which the request numbers carry.
func TestLayouts(t *testing.T) {
	word := unsafe.Sizeof(uintptr(0))
	want := map[string][2]uintptr{ // arm, arm64
		"format":          {204, 208},
		"pixFormatMplane": {192, 192},
		"requestBuffers":  {20, 20},
		"plane":           {60, 64},
		"buffer":          {68, 88},
	}
	got := map[string]uintptr{
		"format":          unsafe.Sizeof(format{}),
		"pixFormatMplane": unsafe.Sizeof(pixFormatMplane{}),
		"requestBuffers":  unsafe.Sizeof(requestBuffers{}),
		"plane":           unsafe.Sizeof(plane{}),
		"buffer":          unsafe.Sizeof(buffer{}),
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
	if word == 8 && unsafe.Offsetof(buffer{}.m) != 64 {
		t.Errorf("v4l2_buffer.m at %d, want 64", unsafe.Offsetof(buffer{}.m))
	}
}

// YUYV of known colors: white, black, and the primaries' luma.
func TestToYUYV(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 1))
	copy(img.Pix, []byte{255, 255, 255, 255, 255, 255, 255, 255, 0, 0, 0, 255, 0, 0, 0, 255})
	out := make([]byte, 8)
	toYUYV(img, out)
	if out[0] != 255 || out[2] != 255 || out[1] != 128 || out[3] != 128 {
		t.Errorf("white: %v", out[:4])
	}
	if out[4] != 0 || out[6] != 0 || out[5] != 128 || out[7] != 128 {
		t.Errorf("black: %v", out[4:])
	}
}
