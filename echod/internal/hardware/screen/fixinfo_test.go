//go:build !dot && !panel

package screen

import (
	"strconv"
	"testing"
	"unsafe"
)

// The kernel copies its own fb_fix_screeninfo into ours whatever size ours is, so the sizes must
// match: 68 bytes on 32-bit, 80 on arm64.
func TestFixInfoSize(t *testing.T) {
	want := uintptr(68)
	if strconv.IntSize == 64 {
		want = 80
	}
	if got := unsafe.Sizeof(fixInfo{}); got != want {
		t.Fatalf("fixInfo is %d bytes, want %d", got, want)
	}
}

// The fields read from fb_fix_screeninfo sit after its first unsigned long, so their offsets move with
// it: line_length is at 44 on 32-bit and 48 on arm64.
func TestFixInfoOffsets(t *testing.T) {
	var fx fixInfo
	want := map[int][3]uintptr{32: {20, 44, 48}, 64: {24, 48, 56}}[strconv.IntSize]
	got := [3]uintptr{unsafe.Offsetof(fx.SmemLen), unsafe.Offsetof(fx.LineLength), unsafe.Offsetof(fx.MmioStart)}
	if got != want {
		t.Fatalf("smem_len, line_length, mmio_start at %v, want %v", got, want)
	}
}
