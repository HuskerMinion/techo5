//go:build !dot && !spot

package camera

import (
	"strconv"
	"testing"
	"unsafe"
)

// struct v4l2_format's union is pointer aligned, so the struct and VIDIOC_S_FMT's number differ
// between a 32-bit and a 64-bit build. Pinned to what the kernel headers give on each.
func TestV4L2FormatLayout(t *testing.T) {
	want := map[int]struct{ size, union, sfmt uintptr }{
		32: {204, 4, 0xc0cc5605},
		64: {208, 8, 0xc0d05605},
	}[strconv.IntSize]
	var f v4l2Format
	if got := unsafe.Sizeof(f); got != want.size {
		t.Errorf("sizeof(v4l2_format) = %d, want %d", got, want.size)
	}
	if got := unsafe.Offsetof(f.union); got != want.union {
		t.Errorf("fmt union at %d, want %d", got, want.union)
	}
	if got := unsafe.Offsetof(f.union.pix.sizeimage); got != 20 {
		t.Errorf("sizeimage at %d into the union, want 20", got)
	}
	if vidiocSFmt != want.sfmt {
		t.Errorf("VIDIOC_S_FMT = %#x, want %#x", vidiocSFmt, want.sfmt)
	}
	if vidiocSCtrl != 0xc008561c {
		t.Errorf("VIDIOC_S_CTRL = %#x, want 0xc008561c", vidiocSCtrl)
	}
}
