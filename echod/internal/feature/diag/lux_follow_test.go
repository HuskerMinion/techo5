package diag

import (
	"testing"
	"time"
)

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

// A lamp switched on in a steady room goes at once; once readings come close together, a quarter
// minute apart, not every two seconds.
func TestLuxGap(t *testing.T) {
	if g := luxGap(0); g != luxEvery {
		t.Errorf("first reading: %v, want %v", g, luxEvery)
	}
	if g := luxGap(10 * time.Minute); g != luxEvery {
		t.Errorf("after a steady spell: %v, want %v", g, luxEvery)
	}
	if g := luxGap(3 * time.Second); g != luxBusy {
		t.Errorf("readings 3 s apart: %v, want %v", g, luxBusy)
	}
}
