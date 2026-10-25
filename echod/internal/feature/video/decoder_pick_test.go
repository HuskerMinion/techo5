//go:build !dot

package video

import "testing"

// The 64-bit build only on a 64-bit kernel, and only where the image carries it: athena on Amazon's
// 32-bit kernel, and every other board, keep the 32-bit one.
func TestPickDecoder(t *testing.T) {
	for _, c := range []struct {
		machine string
		have64  bool
		want    string
	}{
		{"aarch64", true, decoder64},
		{"aarch64", false, decoder32},
		{"armv8l", true, decoder32},
		{"armv7l", true, decoder32},
		{"", true, decoder32},
	} {
		if got := pickDecoder(c.machine, c.have64); got != c.want {
			t.Errorf("pickDecoder(%q, %v) = %s, want %s", c.machine, c.have64, got, c.want)
		}
	}
}
