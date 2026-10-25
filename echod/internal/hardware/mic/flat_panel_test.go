//go:build panel

package mic

import (
	"math/rand"
	"testing"
)

func pcm(samples []int16) []byte {
	b := make([]byte, 2*len(samples))
	for i, v := range samples {
		b[2*i], b[2*i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	return b
}

func seconds(n int, sample func(i int) int16) []byte {
	s := make([]int16, n*Rate)
	for i := range s {
		s[i] = sample(i)
	}
	return pcm(s)
}

// Zeros for flatAfter seconds are silence, said once; audio coming back clears it at once.
func TestZerosAreSilenceUntilSoundReturns(t *testing.T) {
	var w flatWatch
	if s, ch := w.add(seconds(flatAfter-1, func(int) int16 { return 0 })); s || ch {
		t.Fatalf("silent after %d seconds of zeros", flatAfter-1)
	}
	if s, ch := w.add(seconds(1, func(int) int16 { return 0 })); !s || !ch {
		t.Fatalf("not silent after %d seconds of zeros", flatAfter)
	}
	if s, ch := w.add(seconds(5, func(int) int16 { return 0 })); !s || ch {
		t.Fatal("silence said again, or lost")
	}
	rng := rand.New(rand.NewSource(1))
	if s, ch := w.add(seconds(1, func(int) int16 { return int16(rng.NormFloat64() * 300) })); s || !ch {
		t.Fatal("still silent with sound back")
	}
}

// A stream stuck at one value, or wobbling by a step, is as flat as zeros.
func TestAStuckStreamIsSilence(t *testing.T) {
	var w flatWatch
	if s, _ := w.add(seconds(flatAfter, func(i int) int16 { return int16(-37 + i%2) })); !s {
		t.Fatal("a stream stuck near one value is not silence")
	}
}

// A quiet room is not silence: a microphone's own noise, here about -96 dBFS (one step RMS), is wider
// than flatSpan every second.
func TestAQuietRoomIsNotSilence(t *testing.T) {
	var w flatWatch
	rng := rand.New(rand.NewSource(2))
	if s, _ := w.add(seconds(3*flatAfter, func(int) int16 { return int16(rng.NormFloat64()) })); s {
		t.Fatal("a quiet room's noise was taken for silence")
	}
}

// One second of sound in the middle starts the count again.
func TestSoundInTheMiddleStartsTheCountAgain(t *testing.T) {
	var w flatWatch
	w.add(seconds(flatAfter-2, func(int) int16 { return 0 }))
	w.add(seconds(1, func(i int) int16 { return int16(i % 200) }))
	if s, _ := w.add(seconds(flatAfter-1, func(int) int16 { return 0 })); s {
		t.Fatal("silent before flatAfter seconds in a row")
	}
}

// A new connection seeded silent stays silent through more zeros and clears, saying so, at the first
// sound; seeded hearing, it starts the count from nothing.
func TestASeededWatchClearsOnSound(t *testing.T) {
	var w flatWatch
	w.seed(true)
	if s, ch := w.add(seconds(2, func(int) int16 { return 0 })); !s || ch {
		t.Fatalf("seeded silent, zeros: silent %v changed %v", s, ch)
	}
	if s, ch := w.add(seconds(1, func(i int) int16 { return int16(i % 300) })); s || !ch {
		t.Fatalf("seeded silent, sound: silent %v changed %v", s, ch)
	}

	var v flatWatch
	v.seed(false)
	if s, _ := v.add(seconds(flatAfter-1, func(int) int16 { return 0 })); s {
		t.Fatal("seeded hearing, silent too soon")
	}
}
