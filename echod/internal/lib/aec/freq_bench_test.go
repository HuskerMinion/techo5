package aec

import (
	"math/rand"
	"testing"
)

func benchFrames() ([]int16, []int16) {
	rng := rand.New(rand.NewSource(3))
	mic, ref := make([]int16, 320), make([]int16, 320)
	for i := range mic {
		mic[i], ref[i] = int16(rng.Intn(4000)-2000), int16(rng.Intn(4000)-2000)
	}
	return mic, ref
}

// One 20 ms frame. 50 a second is real time.
func BenchmarkFreqFrame(b *testing.B) {
	f, _ := NewFreq(1024, 16000)
	mic, ref := benchFrames()
	for b.Loop() {
		_, _ = f.Process(mic, ref)
	}
}

func BenchmarkTimeDomainFrame(b *testing.B) {
	f, _ := New(Config{Taps: 1024, Mu: 0.1})
	mic, ref := benchFrames()
	for b.Loop() {
		_, _ = f.Process(mic, ref)
	}
}

func BenchmarkTimeDomain256Frame(b *testing.B) {
	f, _ := New(Config{Taps: 256, Mu: 0.1})
	mic, ref := benchFrames()
	for b.Loop() {
		_, _ = f.Process(mic, ref)
	}
}
