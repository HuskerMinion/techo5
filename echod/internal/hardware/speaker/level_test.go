package speaker

import (
	"encoding/binary"
	"math"
	"testing"
)

// periodOf is one interleaved stereo period of 16-bit samples, every frame at v.
func periodOf(v int16) []byte {
	buf := make([]byte, 256*Channels*Bits/8)
	for i := 0; i+1 < len(buf); i += 2 {
		binary.LittleEndian.PutUint16(buf[i:], uint16(v))
	}
	return buf
}

func TestLevelFollowsThePeakAndFallsAway(t *testing.T) {
	p := &Player{}
	if got := p.Level(); got != 0 {
		t.Fatalf("a new player's level is %v, want 0", got)
	}

	// A loud period is taken at once, and a negative peak counts the same as a positive one.
	p.meter(periodOf(-16384))
	if got := p.Level(); math.Abs(got-0.5) > 0.001 {
		t.Fatalf("after a half-scale period the level is %v, want 0.5", got)
	}

	// Silence does not drop it to zero: each quiet period keeps levelFall of the last reading.
	p.meter(periodOf(0))
	if got, want := p.Level(), 0.5*levelFall; math.Abs(got-want) > 0.001 {
		t.Fatalf("one quiet period after half scale: %v, want %v", got, want)
	}
	for i := 0; i < 60; i++ {
		p.meter(periodOf(0))
	}
	if got := p.Level(); got > 0.001 {
		t.Fatalf("after 60 quiet periods the level is still %v", got)
	}

	// Full scale reads as 1, and a louder period replaces a falling reading rather than adding to it.
	p.meter(periodOf(math.MaxInt16))
	if got := p.Level(); math.Abs(got-1) > 0.001 {
		t.Fatalf("a full-scale period reads %v, want 1", got)
	}
}
