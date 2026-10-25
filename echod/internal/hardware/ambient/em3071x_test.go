//go:build !dot

package ambient

import "testing"

func TestEMCount(t *testing.T) {
	for _, c := range []struct {
		lo, hi uint8
		want   int
	}{{0, 0, 0}, {0xFF, 0x0F, 4095}, {0x34, 0x02, 0x234}, {0x80, 0xF1, 0x180}} {
		if got := emCount(c.lo, c.hi); got != c.want {
			t.Errorf("emCount(%#x, %#x) = %d, want %d", c.lo, c.hi, got, c.want)
		}
	}
}
