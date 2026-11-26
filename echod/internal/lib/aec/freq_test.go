package aec

import (
	"math"
	"math/rand"
	"testing"
)

// A colored reference through a short room, the way music reaches a microphone: the frequency-domain
// filter takes the echo off the top of the band as well as the bottom, where the time-domain one,
// normalized by the whole reference's power, is left far behind in the same time.
func TestFreqCancelsTheTopOfTheBandToo(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const n = 16000 * 8
	ref := make([]int16, n)
	var lp float64
	for i := range ref {
		lp = 0.97*lp + rng.NormFloat64() // most of the power low, like music
		ref[i] = int16(max(-32767, min(32767, lp*600+rng.NormFloat64()*60)))
	}
	h := make([]float64, 300)
	h[8] = 0.3
	for k := 9; k < len(h); k++ {
		h[k] = rng.NormFloat64() * 0.05 * math.Exp(-float64(k)/60)
	}
	mic := make([]int16, n)
	for i := range mic {
		var acc float64
		for k, c := range h {
			if i-k >= 0 {
				acc += c * float64(ref[i-k])
			}
		}
		mic[i] = int16(acc + rng.NormFloat64()*2)
	}
	// High-passed residual power over the last two seconds, against the microphone's.
	top := func(x []int16) float64 {
		var s, prev float64
		for _, v := range x[n-32000:] {
			d := float64(v) - prev
			prev = float64(v)
			s += d * d
		}
		return s
	}
	run := func(f interface {
		Process(mic, ref []int16) ([]int16, error)
	}) float64 {
		out := make([]int16, 0, n)
		for at := 0; at+320 <= n; at += 320 {
			e, err := f.Process(mic[at:at+320], ref[at:at+320])
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, e...)
		}
		return 10 * math.Log10(top(mic)/top(out))
	}
	fq, err := NewFreq(1024, 16000)
	if err != nil {
		t.Fatal(err)
	}
	td, _ := New(Config{Taps: 1024, Mu: 0.1})
	got, old := run(fq), run(td)
	t.Logf("echo taken off the top of the band: frequency domain %.1f dB, time domain %.1f dB", got, old)
	if got < 20 || got < old+5 {
		t.Errorf("frequency domain %.1f dB against time domain %.1f dB", got, old)
	}
	if fq.ERLE() < 10 {
		t.Errorf("ERLE %.1f dB", fq.ERLE())
	}
}

func TestFreqTakesWholeBlocksOnly(t *testing.T) {
	f, _ := NewFreq(1024, 16000)
	if _, err := f.Process(make([]int16, 100), make([]int16, 100)); err != ErrLength {
		t.Errorf("a frame of 100: %v", err)
	}
	if f.Taps() != 1024 {
		t.Errorf("taps %d", f.Taps())
	}
	if _, err := NewFreq(0, 16000); err == nil {
		t.Error("no taps accepted")
	}
}

// Silence, full scale, a reference with no echo and an echo with no reference: nothing goes non-finite
// and the filter keeps canceling afterwards.
func TestFreqStaysFiniteUnderAdversarialInput(t *testing.T) {
	f, _ := NewFreq(1024, 16000)
	rng := rand.New(rand.NewSource(2))
	mic, ref := make([]int16, 320), make([]int16, 320)
	for round := 0; round < 2000; round++ {
		for i := range mic {
			switch round % 5 {
			case 0:
				mic[i], ref[i] = 0, 0
			case 1:
				mic[i], ref[i] = int16(rng.Intn(65535)-32767), int16(rng.Intn(65535)-32767)
			case 2:
				mic[i], ref[i] = 0, int16(rng.Intn(65535)-32767)
			case 3:
				mic[i], ref[i] = int16(rng.Intn(65535)-32767), 0
			default:
				mic[i], ref[i] = 32767, -32768
			}
		}
		if _, err := f.Process(mic, ref); err != nil {
			t.Fatal(err)
		}
	}
	for _, w := range append(f.bg, f.fg...) {
		for _, v := range w {
			if math.IsNaN(real(v)) || math.IsInf(real(v), 0) || math.IsNaN(imag(v)) || math.IsInf(imag(v), 0) {
				t.Fatal("the filter went non-finite")
			}
		}
	}
	if e := f.ERLE(); math.IsNaN(e) || math.IsInf(e, 0) {
		t.Fatalf("ERLE %v", e)
	}
}

func TestFreqNeedsARate(t *testing.T) {
	if _, err := NewFreq(1024, 0); err == nil {
		t.Error("no rate accepted")
	}
}

// A clipped microphone sample holds learning off for a filter's length and one block more, and
// what the filter knew stays.
func TestFreqHoldsOffAfterAClip(t *testing.T) {
	f, _ := NewFreq(256, 16000)
	mic, ref := make([]int16, freqBlock), make([]int16, freqBlock)
	for i := range mic {
		mic[i], ref[i] = int16(i*100), int16(i*100)
	}
	mic[3] = 32000
	if _, err := f.Process(mic, ref); err != nil {
		t.Fatal(err)
	}
	// The block that clipped already counts one down.
	if f.saturated != f.parts {
		t.Fatalf("saturated %d after a clip, want %d", f.saturated, f.parts)
	}
	mic[3] = 0
	for i := 0; i < f.parts; i++ {
		if _, err := f.Process(mic, ref); err != nil {
			t.Fatal(err)
		}
	}
	if f.saturated != 0 {
		t.Errorf("saturated %d a filter's length later", f.saturated)
	}
	if f.block != 0 {
		t.Errorf("%d updates while held off", f.block)
	}
}

// A filter that keeps adding echo starts over, as SpeexDSP's does.
func TestFreqStartsOverWhenItAddsEcho(t *testing.T) {
	f, _ := NewFreq(256, 16000)
	rng := rand.New(rand.NewSource(3))
	mic, ref := make([]int16, freqBlock), make([]int16, freqBlock)
	for i := range ref {
		ref[i] = int16(rng.NormFloat64() * 3000)
	}
	// A foreground that turns the reference into ten times itself on a silent microphone.
	for p := range f.fg {
		for k := range f.fg[p] {
			f.fg[p][k] = 0
		}
	}
	for k := range f.fg[0] {
		f.fg[0][k] = 10
		f.bg[0][k] = 10
	}
	for i := 0; i < freqScrewedUp; i++ {
		if _, err := f.Process(mic, ref); err != nil {
			t.Fatal(err)
		}
		if f.fg[0][0] == 0 {
			return // started over
		}
	}
	t.Errorf("still the bad filter after %d blocks of added echo (count %d)", freqScrewedUp, f.screwedUp)
}
