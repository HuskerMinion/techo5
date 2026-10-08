package alsa

import "testing"

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
