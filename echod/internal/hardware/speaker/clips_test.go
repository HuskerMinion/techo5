package speaker

import (
	"math"
	"testing"
	"time"
)

// Every recorded sound is there, as long as it was recorded, and louder levels make it louder but
// never past full scale: a clip mixed near the top has nowhere left to go.
func TestClipsPlay(t *testing.T) {
	for _, c := range []*Clip{ClipWake, ClipTimer, ClipMuteOn, ClipMuteOff} {
		if c.Ms() < 300 || c.Ms() > 4000 {
			t.Errorf("%s lasts %d ms", c.file, c.Ms())
		}
		quiet, loud := tone(c.Note(), toneLevel), tone(c.Note(), 1)
		if len(quiet) != len(c.samples)*Channels {
			t.Errorf("%s rendered %d samples for %d", c.file, len(quiet), len(c.samples))
		}
		var peakQuiet, peakLoud int
		for i := range quiet {
			peakQuiet = max(peakQuiet, abs(int(quiet[i])))
			peakLoud = max(peakLoud, abs(int(loud[i])))
		}
		if peakLoud < peakQuiet || peakLoud > math.MaxInt16 {
			t.Errorf("%s peaks %d quiet, %d loud", c.file, peakQuiet, peakLoud)
		}
	}
}

// The wake sound is loud for its first few hundred milliseconds and a fade after that, and a turn
// waits out only the loud part: holding the microphone back for the whole of it lost people's first
// words. A tone's audible length is its length.
func TestTheWakeSoundIsLoudOnlyBriefly(t *testing.T) {
	loud := Audible([]Note{ClipWake.Note()})
	if loud < 150*time.Millisecond || loud > 450*time.Millisecond {
		t.Errorf("the wake sound holds the microphone for %v", loud)
	}
	if Audible(ToneMute) != Length(ToneMute) {
		t.Error("a tone's audible length is not its length")
	}
}

