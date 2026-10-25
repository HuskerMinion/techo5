//go:build !dot && !spot && !panel

package mic

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/audio"
)

// The channel the echo canceller reads is the mix of the two speakers, and in mono, where the two
// are the same, it is exactly what was played.
func TestPutRefMixesTheFirstChannel(t *testing.T) {
	frame := make([]byte, 3*(RefFirst+2))
	var meter [athenaRefChannels]float64
	read := func(ch int) int32 { return audio.DecodeS24LE3(frame[3*(RefFirst+ch):]) }

	putRef(frame, 0.5, 0, &meter)
	if got, want := read(0), int32(0.25*(1<<23)); got != want {
		t.Errorf("stereo: first channel %d, want the mix %d", got, want)
	}
	if read(1) != 0 {
		t.Errorf("stereo: second channel %d, want it as played", read(1))
	}
	putRef(frame, 0.3, 0.3, &meter)
	if a, b := read(0), read(1); a != b {
		t.Errorf("mono: channels %d and %d, want the same", a, b)
	}
}
