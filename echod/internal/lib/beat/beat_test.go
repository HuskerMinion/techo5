package beat

import (
	"math"
	"testing"
)

const (
	rate  = 48000
	block = 1024
)

// feed runs a generator for seconds in speaker-sized blocks and returns the final snapshot.
func feed(a *Analyzer, seconds float64, sample func(t float64) float64) Snapshot {
	n := int(seconds * rate)
	buf := make([]int16, block*2)
	for at := 0; at < n; at += block {
		for i := 0; i < block; i++ {
			v := int16(sample(float64(at+i)/rate) * 32767)
			buf[i*2], buf[i*2+1] = v, v
		}
		a.Feed(buf, nil, rate, block)
	}
	return a.Snapshot()
}

func tone(hz, amp float64) func(float64) float64 {
	return func(t float64) float64 { return amp * math.Sin(2*math.Pi*hz*t) }
}

func TestSilence(t *testing.T) {
	var a Analyzer
	s := feed(&a, 3, func(float64) float64 { return 0 })
	if s.Playing || s.Level != 0 || s.Beats != 0 {
		t.Fatalf("silence: %+v", s)
	}
}

func TestSteadyToneHasLevelAndNoBeats(t *testing.T) {
	var a Analyzer
	s := feed(&a, 4, tone(880, 0.25))
	if !s.Playing || s.Level < 0.8 {
		t.Fatalf("tone: %+v", s)
	}
	if s.Mid <= s.Bass || s.Mid <= s.Treble {
		t.Fatalf("880 Hz should be mostly mid: %+v", s)
	}
	if s.Beats != 0 {
		t.Fatalf("steady tone made %d beats", s.Beats)
	}
}

func TestBandsSeparate(t *testing.T) {
	var a Analyzer
	if s := feed(&a, 3, tone(70, 0.25)); s.Bass <= s.Mid || s.Bass <= s.Treble {
		t.Fatalf("70 Hz: %+v", s)
	}
	a.Reset()
	if s := feed(&a, 3, tone(4500, 0.25)); s.Treble <= s.Bass {
		t.Fatalf("4.5 kHz: %+v", s)
	}
}

func TestStopsAfterSilence(t *testing.T) {
	var a Analyzer
	feed(&a, 2, tone(440, 0.3))
	s := feed(&a, 2, func(float64) float64 { return 0 })
	if s.Playing || s.Level > 0.01 {
		t.Fatalf("after silence: %+v", s)
	}
}

// kick is a decaying 60 Hz thump on every beat of a tempo, over a quiet steady bed.
func kick(bpm float64) func(float64) float64 {
	period := 60 / bpm
	return func(t float64) float64 {
		since := math.Mod(t, period)
		v := 0.05 * math.Sin(2*math.Pi*330*t)
		return v + 0.6*math.Exp(-since/0.05)*math.Sin(2*math.Pi*60*since)
	}
}

func TestKickTrackBeats(t *testing.T) {
	var a Analyzer
	feed(&a, 2, kick(120)) // settle
	before := a.Snapshot().Beats
	feed(&a, 10, kick(120))
	got := a.Snapshot().Beats - before
	if got < 18 || got > 22 {
		t.Fatalf("120 bpm over 10 s: %d beats, want about 20", got)
	}
}

func TestClickTrackBeats(t *testing.T) {
	click := func(t float64) float64 {
		since := math.Mod(t, 0.5)
		return 0.8 * math.Exp(-since/0.01) * math.Sin(2*math.Pi*150*since)
	}
	var a Analyzer
	feed(&a, 2, click)
	before := a.Snapshot().Beats
	feed(&a, 10, click)
	got := a.Snapshot().Beats - before
	if got < 18 || got > 22 {
		t.Fatalf("click track: %d beats, want about 20", got)
	}
}

func TestBeatFades(t *testing.T) {
	var a Analyzer
	s := feed(&a, 1.2, kick(120))
	// Right after a kick the envelope is up; half a second later with the same kick it has cycled.
	_ = s
	quiet := feed(&a, 1, tone(330, 0.05))
	if quiet.Beat > 0.05 {
		t.Fatalf("beat did not fade: %+v", quiet)
	}
}

func BenchmarkFeed(b *testing.B) {
	var a Analyzer
	buf := make([]int16, block*2)
	for i := range buf {
		buf[i] = int16(8000 * math.Sin(float64(i)*0.05))
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.Feed(buf, nil, rate, block)
	}
}
