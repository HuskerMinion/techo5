package mic

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

// roomFrame is a frame of quiet room noise, and toneFrame one of somebody speaking up: a tone in the
// band the level is measured in.
func roomFrame(r *rand.Rand) []int16 {
	f := make([]int16, FrameSamples)
	for i := range f {
		f[i] = int16(r.IntN(81) - 40)
	}
	return f
}

func toneFrame(nth int, amp float64) []int16 {
	f := make([]int16, FrameSamples)
	for i := range f {
		at := float64(nth*FrameSamples+i) / Rate
		f[i] = int16(amp * math.Sin(2*math.Pi*500*at))
	}
	return f
}

func TestSpeechStandsOutFromTheRoom(t *testing.T) {
	var s standout
	s.on.Store(true)
	r := rand.New(rand.NewPCG(1, 2))
	least := quietest(20)
	for range 100 { // two seconds of the room
		s.add(roomFrame(r), least)
	}
	quiet := s.best(time.Second)
	for i := range 25 { // half a second of a voice
		s.add(toneFrame(i, 4000), least)
	}
	loud := s.best(time.Second)
	if quiet > 6 {
		t.Errorf("the room alone stands %.1f dB over itself, want little", quiet)
	}
	if loud < 25 {
		t.Errorf("a voice stands %.1f dB over the room, want well over 25", loud)
	}

	// Louder speech stands out further: the device nearer the speaker wins.
	var near standout
	near.on.Store(true)
	r = rand.New(rand.NewPCG(1, 2))
	for range 100 {
		near.add(roomFrame(r), least)
	}
	for i := range 25 {
		near.add(toneFrame(i, 12000), least)
	}
	if got := near.best(time.Second); got <= loud {
		t.Errorf("speech three times louder stands %.1f dB out, no more than %.1f", got, loud)
	}
}

func TestStandoutIsNothingWhileOff(t *testing.T) {
	var s standout
	for i := range 50 {
		s.add(toneFrame(i, 8000), quietest(20))
	}
	if got := s.best(time.Second); got != 0 {
		t.Errorf("measured %.1f while off", got)
	}
}

// A room measured with no floor to hold it up still gives a number a claim can carry.
func TestStandoutIsAlwaysANumber(t *testing.T) {
	var s standout
	s.on.Store(true)
	for i := range 20 {
		s.add(make([]int16, FrameSamples), 0)
		s.add(toneFrame(i, 8000), 0)
	}
	got := s.best(time.Second)
	if math.IsInf(got, 0) || math.IsNaN(got) || got > standoutMax {
		t.Errorf("stood out %v", got)
	}
}

// Turned on again, it does not report what it heard before it was turned off.
func TestStandoutStartsAfresh(t *testing.T) {
	s := New()
	s.SetStandout(true)
	for i := range 25 {
		s.standout.add(toneFrame(i, 8000), quietest(20))
	}
	s.SetStandout(false)
	s.SetStandout(true)
	if got := s.Standout(time.Second); got != 0 {
		t.Errorf("after turning it off and on, it reports %.1f from before", got)
	}
}

// Turned on again, the floor is learned again rather than carried over from the room it was in before.
func TestStandoutRelearnsTheFloor(t *testing.T) {
	s := New()
	s.SetStandout(true)
	r := rand.New(rand.NewPCG(3, 4))
	for range 100 {
		s.standout.add(roomFrame(r), quietest(20))
	}
	s.SetStandout(false)
	s.SetStandout(true)
	s.standout.add(roomFrame(r), quietest(20))
	if s.standout.fresh.Load() {
		t.Error("the reader did not take the request to start afresh")
	}
	if s.standout.n != 1 {
		t.Errorf("the reader carried %d frames over into the new block, want just this one", s.standout.n)
	}
}

// The mean is against each device's own floor: a mic with four times the gain hears the same word the
// same, and a nearer, louder voice stands further out. The room is turned up so its floor is its own,
// not the least the converter allows.
func TestTheMeanIgnoresMicGain(t *testing.T) {
	mean := func(gain, voice float64) float64 {
		var s standout
		s.on.Store(true)
		r := rand.New(rand.NewPCG(1, 2))
		least := quietest(20)
		for range 100 {
			f := roomFrame(r)
			for i := range f {
				f[i] = int16(float64(f[i]) * gain)
			}
			s.add(f, least)
		}
		for i := range 25 {
			s.add(toneFrame(i, voice*gain), least)
		}
		_, m := s.over(time.Second)
		return m
	}
	quiet, hot := mean(4, 500), mean(16, 500)
	if math.Abs(quiet-hot) > 1 {
		t.Errorf("four times the gain moves the mean from %.1f to %.1f dB, want within 1", quiet, hot)
	}
	if near := mean(4, 1500); near <= quiet+3 {
		t.Errorf("a voice three times louder has a mean of %.1f dB, want well over %.1f", near, quiet)
	}
}
