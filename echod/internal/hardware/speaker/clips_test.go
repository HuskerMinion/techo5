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
		if peakLoud < peakQuiet || float64(peakLoud) > 0.98*math.MaxInt16+1 {
			t.Errorf("%s peaks %d quiet, %d loud: past 0.98 of full scale", c.file, peakQuiet, peakLoud)
		}
	}
}

// The wake sound is loud for its first few hundred milliseconds and a fade after that, and a turn
// waits out only the loud part: holding the microphone back for the whole of it lost people's first
// words. A tone's audible length is its length.
func TestTheWakeSoundIsLoudOnlyBriefly(t *testing.T) {
	loud := Audible([]Note{ClipWake.Note()}, false)
	if loud < 150*time.Millisecond || loud > 450*time.Millisecond {
		t.Errorf("the wake sound holds the microphone for %v", loud)
	}
	if bare := Audible([]Note{ClipWake.Note()}, true); bare <= loud || bare > time.Duration(ClipWake.Ms())*time.Millisecond {
		t.Errorf("with no canceller the wake sound holds it for %v, against %v with one", bare, loud)
	}
	if Audible(ToneMute, false) != Length(ToneMute) || Audible(ToneMute, true) != Length(ToneMute) {
		t.Error("a tone's audible length is not its length")
	}
}

// Listing the sounds decodes none of them: a clip's length comes from its file.
func TestAClipsLengthNeedsNoDecoding(t *testing.T) {
	c := &Clip{file: "mute_switch_on"}
	if ms := c.Ms(); ms < 300 || c.samples != nil {
		t.Errorf("length %d ms, decoded %v", ms, c.samples != nil)
	}
}
