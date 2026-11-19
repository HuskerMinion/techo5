package display

import (
	"testing"
	"time"
)

func TestClockFormats(t *testing.T) {
	at := time.Date(2026, 9, 18, 15, 4, 0, 0, time.UTC)
	defer clock24.Store(false)
	defer noAMPM.Store(false)
	for _, c := range []struct {
		h24, bare           bool
		hm, suffix, inALine string
	}{
		{false, false, "3:04", "PM", "3:04 PM"},
		{true, false, "15:04", "", "15:04"},
		// No AM/PM leaves it off the clock face but not out of a line of text, where 3:04 could be either.
		{false, true, "3:04", "", "3:04 PM"},
		{true, true, "15:04", "", "15:04"},
	} {
		clock24.Store(c.h24)
		noAMPM.Store(c.bare)
		if got := clockHM(at); got != c.hm {
			t.Errorf("24h=%v clockHM = %q, want %q", c.h24, got, c.hm)
		}
		if got := clockSuffix(at); got != c.suffix {
			t.Errorf("24h=%v clockSuffix = %q, want %q", c.h24, got, c.suffix)
		}
		if got := clockText(at); got != c.inALine {
			t.Errorf("24h=%v clockText = %q, want %q", c.h24, got, c.inALine)
		}
	}
}
