//go:build !dot

package video

import "testing"

// The 64-bit build only on a 64-bit kernel, and only where the image carries it: athena on Amazon's
// 32-bit kernel, and every other board, keep the 32-bit one. The distribution's ffmpeg only where the
// image carries neither.
func TestPickDecoder(t *testing.T) {
	for _, c := range []struct {
		machine    string
		have64     bool
		have32     bool
		haveSystem bool
		want       string
	}{
		{"aarch64", true, true, false, decoder64},
		{"aarch64", false, true, false, decoder32},
		{"armv8l", true, true, false, decoder32},
		{"armv7l", true, true, false, decoder32},
		{"", true, true, false, decoder32},
		{"aarch64", true, true, true, decoder64},
		{"armv7l", false, true, true, decoder32},
		{"armv7l", false, false, true, systemDecoder},
		{"aarch64", false, false, true, systemDecoder},
		{"armv7l", false, false, false, decoder32},
	} {
		if got := pickDecoder(c.machine, c.have64, c.have32, c.haveSystem); got != c.want {
			t.Errorf("pickDecoder(%q, %v, %v, %v) = %s, want %s", c.machine, c.have64, c.have32, c.haveSystem, got, c.want)
		}
	}
}
