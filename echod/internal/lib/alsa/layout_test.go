package alsa

import (
	"encoding/binary"
	"testing"
	"unsafe"
)

// The structures whose size depends on the kernel's long, and the ioctl numbers that encode them,
// pinned to what the kernel headers give on a 32-bit and a 64-bit build.
func TestWordSizedLayouts(t *testing.T) {
	want := map[int]struct {
		hwParams, xferi, elemValue uintptr
		hwParamsNr, readi, writei  uintptr
		elemRead, elemWrite        uintptr
	}{
		4: {604, 12, 712, 0xc25c4111, 0x800c4151, 0x400c4150, 0xc2c85512, 0xc2c85513},
		8: {608, 24, 1224, 0xc2604111, 0x80184151, 0x40184150, 0xc4c85512, 0xc4c85513},
	}[longSize]
	if want.hwParams == 0 {
		t.Fatalf("no reference layout for a %d-byte long", longSize)
	}
	for _, c := range []struct {
		what      string
		got, want uintptr
	}{
		{"sizeof(snd_pcm_hw_params)", hwParamsSize, want.hwParams},
		{"sizeof(snd_xferi)", xferiSize, want.xferi},
		{"xferi struct", unsafe.Sizeof(xferi{}), want.xferi},
		{"xferi.buf offset", unsafe.Offsetof(xferi{}.buf), longSize},
		{"sizeof(snd_ctl_elem_value)", elemValueSize, want.elemValue},
		{"SNDRV_PCM_IOCTL_HW_PARAMS", ioctlHwParams, want.hwParamsNr},
		{"SNDRV_PCM_IOCTL_READI_FRAMES", ioctlReadi, want.readi},
		{"SNDRV_PCM_IOCTL_WRITEI_FRAMES", ioctlWritei, want.writei},
		{"SNDRV_CTL_IOCTL_ELEM_INFO", ioctlElemInfo, 0xc1105511},
		{"SNDRV_CTL_IOCTL_ELEM_READ", ioctlElemRead, want.elemRead},
		{"SNDRV_CTL_IOCTL_ELEM_WRITE", ioctlElemWrite, want.elemWrite},
	} {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", c.what, c.got, c.want)
		}
	}
}

// An integer control's value is a long. A negative level written on a 64-bit build has to fill the
// whole slot, or the kernel reads it as a large positive number.
func TestIntegerValueFillsTheLong(t *testing.T) {
	b := make([]byte, 8)
	putValue(b, TypeInteger, uint32(0xffffffff)) // -1
	if longSize == 8 {
		if got := int64(binary.LittleEndian.Uint64(b)); got != -1 {
			t.Errorf("stored %d, want -1", got)
		}
	} else if got := int32(binary.LittleEndian.Uint32(b)); got != -1 || binary.LittleEndian.Uint32(b[4:]) != 0 {
		t.Errorf("stored %d with %#x past it, want -1 and nothing", got, b[4:])
	}
	clear(b)
	putValue(b, TypeInteger64, uint32(0xffffffff))
	if got := binary.LittleEndian.Uint64(b); got != 0xffffffff {
		t.Errorf("64-bit control stored %#x, want 0xffffffff", got)
	}
}
