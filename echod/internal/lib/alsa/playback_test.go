package alsa

import (
	"testing"
	"unsafe"
)

// SNDRV_PCM_IOCTL_DELAY is _IOR('A', 0x21, snd_pcm_sframes_t), and snd_pcm_sframes_t is the kernel's
// long: the number differs between a 32-bit and a 64-bit build. Pinned to what the kernel headers
// give, so a wrong size or direction shows here rather than as a card that never answers.
func TestDelayIoctlNumber(t *testing.T) {
	want := map[int]uintptr{4: 0x80044121, 8: 0x80084121}[longSize]
	if want == 0 {
		t.Fatalf("no reference number for a %d-byte long", longSize)
	}
	if ioctlDelay != want {
		t.Fatalf("ioctlDelay = %#x, want %#x", ioctlDelay, want)
	}
}

// struct snd_pcm_sw_params: three ints, seven unsigned longs, two ints and 56 reserved bytes. The
// longs start at 12 on a 32-bit build and at 16 on a 64-bit one, and the size is in the ioctl number,
// so both are pinned to what the kernel headers give.
func TestSwParamsLayout(t *testing.T) {
	want := map[int]struct {
		size, start uintptr
		number      uintptr
	}{
		4: {104, 20, 0xc0684113},
		8: {136, 32, 0xc0884113},
	}[longSize]
	if want.size == 0 {
		t.Fatalf("no reference layout for a %d-byte long", longSize)
	}
	var sw swParams
	if got := unsafe.Sizeof(sw); got != want.size {
		t.Errorf("sizeof = %d, want %d", got, want.size)
	}
	if got := unsafe.Offsetof(sw.startThreshold); got != want.start {
		t.Errorf("start_threshold at %d, want %d", got, want.start)
	}
	if ioctlSwParams != want.number {
		t.Errorf("ioctlSwParams = %#x, want %#x", ioctlSwParams, want.number)
	}
}

// A stream that starts full has the whole ring in front of the first frame the card reads, at open
// and after every underrun, rather than one period (#142).
func TestFullStartWaitsForTheWholeRing(t *testing.T) {
	sw := fullStart(Config{PeriodSize: 768, Periods: 4})
	if sw.startThreshold != 3072 || sw.stopThreshold != 3072 {
		t.Errorf("start %d, stop %d, want both 3072", sw.startThreshold, sw.stopThreshold)
	}
	if sw.availMin != 768 || sw.periodStep != 1 {
		t.Errorf("avail_min %d, period_step %d, want 768 and 1", sw.availMin, sw.periodStep)
	}
	if sw.silenceThreshold != 0 || sw.silenceSize != 0 || sw.proto != 0 {
		t.Errorf("silence %d/%d, proto %d, want zeros", sw.silenceThreshold, sw.silenceSize, sw.proto)
	}
}
