//go:build !dot

package diag

import "testing"

// A change in the light is sent when it is worth a look: a tenth of the last reading, and at least
// one lux, so a dark room's 0 to 2 goes and a bright room's flicker does not.
func TestLuxWorthSending(t *testing.T) {
	for _, c := range []struct {
		had, now float64
		want     bool
	}{
		{72, 3, true},
		{0, 2, true},
		{0, 0.5, false},
		{500, 530, false},
		{500, 560, true},
		{3, 72, true},
	} {
		if got := luxWorthSending(c.had, c.now); got != c.want {
			t.Errorf("%v to %v: %v, want %v", c.had, c.now, got, c.want)
		}
	}
}
