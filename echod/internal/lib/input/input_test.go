package input

import (
	"strconv"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// struct input_event starts with a struct timeval, two kernel longs: 16 bytes on a 32-bit build, 24
// on arm64. evdev refuses a read shorter than one event, so the size is checked against x/sys/unix's
// own per-architecture Timeval as well as the numbers the kernel headers give.
func TestEventLayout(t *testing.T) {
	var kernel struct {
		time      unix.Timeval
		typ, code uint16
		value     int32
	}
	if eventSize != unsafe.Sizeof(kernel) {
		t.Errorf("eventSize = %d, sizeof(struct input_event) = %d", eventSize, unsafe.Sizeof(kernel))
	}
	if evType != unsafe.Offsetof(kernel.typ) || evCode != unsafe.Offsetof(kernel.code) || evValue != unsafe.Offsetof(kernel.value) {
		t.Errorf("type/code/value at %d/%d/%d, want %d/%d/%d", evType, evCode, evValue,
			unsafe.Offsetof(kernel.typ), unsafe.Offsetof(kernel.code), unsafe.Offsetof(kernel.value))
	}
	if want := map[int]int{32: 16, 64: 24}[strconv.IntSize]; eventSize != want {
		t.Errorf("eventSize = %d, want %d", eventSize, want)
	}
}

// struct input_absinfo is six s32s on every ABI; EVIOCGABS carries its size.
func TestAbsInfoSize(t *testing.T) {
	if got := unsafe.Sizeof(AbsInfo{}); got != 24 {
		t.Errorf("sizeof(AbsInfo) = %d, want 24", got)
	}
}
