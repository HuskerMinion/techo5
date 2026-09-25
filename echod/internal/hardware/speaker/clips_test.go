package speaker

import (
	"math"
	"testing"
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
