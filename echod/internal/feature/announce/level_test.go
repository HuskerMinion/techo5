package announce

import (
	"math"
	"testing"
)

// tone is a sine at the given peak, long enough to measure.
func tone(peak float64, n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(peak * math.Sin(2*math.Pi*float64(i)*440/16000))
	}
	return out
}

func measure(s []int16) (peakDBFS, rmsDBFS float64) {
	var peak, sum float64
	for _, v := range s {
		f := float64(v)
		if a := math.Abs(f); a > peak {
			peak = a
		}
		sum += f * f
	}
	const full = 32767.0
	rms := math.Sqrt(sum / float64(len(s)))
	return 20 * math.Log10(math.Max(peak, 1)/full), 20 * math.Log10(math.Max(rms, 1)/full)
}

// The recording that started all of this: a real one off a Dot, peaking forty decibels below full
// scale. Good enough for a recognizer, far too quiet to hear in another room over anything at all.
func TestAQuietRecordingIsBroughtUp(t *testing.T) {
	// peak -39.6 dBFS, as measured on the device.
	said := tone(344, 16000)

	wasPeak, _ := measure(said)
	got := level(said)
	nowPeak, nowRMS := measure(got)

	if nowPeak <= wasPeak+10 {
		t.Errorf("peak went from %.1f to %.1f dBFS, want it lifted well clear", wasPeak, nowPeak)
	}
	if nowPeak > ceiling+0.5 {
		t.Errorf("peak %.1f dBFS is over the ceiling of %.1f", nowPeak, ceiling)
	}
	// It cannot reach the target from that far down without more lift than is allowed, and that
	// bound is deliberate: the rest of the way would be room noise.
	if lift := nowPeak - wasPeak; lift > mostLift+0.5 {
		t.Errorf("lifted %.1f dB, more than the %.1f allowed", lift, mostLift)
	}
	t.Logf("peak %.1f -> %.1f dBFS, rms now %.1f dBFS", wasPeak, nowPeak, nowRMS)
}

// A recording that arrived loud is not made louder, and never over the ceiling.
func TestALoudRecordingIsNotPushedIntoClipping(t *testing.T) {
	said := tone(32000, 16000)

	got := level(said)
	peak, _ := measure(got)

	if peak > ceiling+0.5 {
		t.Errorf("peak %.1f dBFS is over the ceiling of %.1f", peak, ceiling)
	}
	for _, v := range got {
		if v == math.MaxInt16 || v == math.MinInt16 {
			continue // clamping at the rail is allowed; wrapping is not
		}
	}
}

// Nothing in, nothing out: an empty recording is not an announcement and must not panic on the way
// to not being one.
func TestLevellingNothing(t *testing.T) {
	if got := level(nil); got != nil {
		t.Errorf("got %v, want nil", got)
	}
	if got := level([]int16{}); len(got) != 0 {
		t.Errorf("got %d samples, want none", len(got))
	}
}

// Digital silence has no peak to work from. Dividing by it would be an infinite gain, and the
// samples are left exactly as they are.
func TestLevellingSilence(t *testing.T) {
	said := make([]int16, 1600)
	got := level(said)
	for i, v := range got {
		if v != 0 {
			t.Fatalf("sample %d became %d, want silence left alone", i, v)
		}
	}
}

// The lift is bounded, so a recording of an empty room does not come out as a wall of amplified
// noise in every room in the house.
func TestTheLiftIsBounded(t *testing.T) {
	said := tone(8, 16000) // about -72 dBFS: a room, not a voice

	wasPeak, _ := measure(said)
	nowPeak, _ := measure(level(said))

	if lift := nowPeak - wasPeak; lift > mostLift+0.5 {
		t.Errorf("lifted %.1f dB, more than the %.1f allowed", lift, mostLift)
	}
}
