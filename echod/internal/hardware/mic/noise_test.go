package mic

import (
	"math"
	"testing"
)

// steady is one frame of the mix at a constant level.
func steady(v int16) []int16 {
	f := make([]int16, FrameSamples)
	for i := range f {
		f[i] = v
	}
	return f
}

func TestTheMeterIsOffUntilTurnedOn(t *testing.T) {
	var m noiseMeter
	m.add(rawFrame(0, false), steady(1000))
	if _, ok := m.read(); ok {
		t.Error("a meter nobody turned on gave a reading")
	}
}

func TestTheRoomReadsInDBFS(t *testing.T) {
	var m noiseMeter
	m.on.Store(true)
	for i := range 10 {
		m.add(rawFrame(i, false), steady(1000))
	}
	got, ok := m.read()
	want := 20 * math.Log10(1000.0/fullScale)
	if !ok || math.Abs(got-want) > 0.01 {
		t.Errorf("got %.2f dBFS (%v), want %.2f", got, ok, want)
	}

	// Reading starts a new window.
	if _, ok := m.read(); ok {
		t.Error("an empty window gave a reading")
	}
}

func TestAWindowWithPlaybackIsThrownAway(t *testing.T) {
	var m noiseMeter
	m.on.Store(true)
	m.add(rawFrame(0, false), steady(1000))
	m.add(rawFrame(1, true), steady(1000))
	m.add(rawFrame(2, false), steady(1000))
	if _, ok := m.read(); ok {
		t.Error("a window the speaker played in gave a reading")
	}
}

func TestTheTailAfterPlaybackIsThrownAwayToo(t *testing.T) {
	var m noiseMeter
	m.on.Store(true)
	m.add(rawFrame(0, true), steady(1000))
	m.read()

	// Still inside the second the room goes on ringing.
	m.add(rawFrame(1, false), steady(1000))
	if _, ok := m.read(); ok {
		t.Error("the frame right after playback gave a reading")
	}

	// Past it.
	for i := range Rate/FrameSamples + 1 {
		m.add(rawFrame(2+i, false), steady(1000))
	}
	m.read()
	m.add(rawFrame(100, false), steady(1000))
	if _, ok := m.read(); !ok {
		t.Error("the room long after playback gave no reading")
	}
}

func TestDigitalSilenceIsNoReading(t *testing.T) {
	var m noiseMeter
	m.on.Store(true)
	m.add(rawFrame(0, false), steady(0))
	if _, ok := m.read(); ok {
		t.Error("microphones reading nothing at all gave a reading")
	}
}

func TestAWindowWithNoReadingSaysWhy(t *testing.T) {
	var m noiseMeter
	m.on.Store(true)
	if _, why := m.measure(); why != NoiseUnheard {
		t.Errorf("an empty window: %q, want %q", why, NoiseUnheard)
	}
	m.add(rawFrame(0, true), steady(1000))
	if _, why := m.measure(); why != NoisePlayed {
		t.Errorf("a window with playback: %q, want %q", why, NoisePlayed)
	}
	m.hold = 0
	m.add(rawFrame(1, false), steady(0))
	if _, why := m.measure(); why != NoiseSilent {
		t.Errorf("a window of zeros: %q, want %q", why, NoiseSilent)
	}
	m.add(rawFrame(2, false), steady(1000))
	if _, why := m.measure(); why != "" {
		t.Errorf("a room: %q, want a reading", why)
	}

	s := New()
	if _, why := s.Noise(); why != NoiseOff {
		t.Errorf("measuring off: %q, want %q", why, NoiseOff)
	}
}
