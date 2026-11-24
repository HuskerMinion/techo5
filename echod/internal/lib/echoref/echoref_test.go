package echoref

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// TestFillLinesUp plays a click through Push, stamped as a card would stamp it, and checks Fill puts
// it where the microphones would hear it, Lead early.
func TestFillLinesUp(t *testing.T) {
	const (
		rate   = 48000
		period = 768
		click  = 20000 // the input frame the click is on
	)
	freshRing(t)
	t0 := time.Now()
	pcm := make([]byte, period*4)
	for n := 0; n < 40; n++ {
		clear(pcm)
		if at := click - n*period; at >= 0 && at < period {
			binary.LittleEndian.PutUint16(pcm[4*at:], uint16(int16(16000)))
			binary.LittleEndian.PutUint16(pcm[4*at+2:], uint16(int16(16000)))
		}
		// A few hundred microseconds of jitter in each reading, as a delay read beside a clock has.
		jitter := time.Duration((n%5)-2) * 150 * time.Microsecond
		end := t0.Add(time.Duration((n+1)*period-1)*time.Second/rate + jitter)
		Push(pcm, rate, end)
	}

	// The click is heard at t0 + click/rate. Read a stretch around it.
	heard := Seconds(t0) + float64(click)/rate
	start := heard - 0.01
	l := make([]float32, Rate/50)
	r := make([]float32, len(l))
	if !Fill(l, r, start) {
		t.Fatal("Fill found nothing")
	}
	peak, at := float32(0), -1
	for i, v := range l {
		if a := float32(math.Abs(float64(v))); a > peak {
			peak, at = a, i
		}
	}
	want := int(math.Round((0.01 - Lead.Seconds()) * Rate))
	if at < want-2 || at > want+2 {
		t.Errorf("click at sample %d of the read, want %d (±2): Lead %v early", at, want, Lead)
	}
	if l[at] != r[at] {
		t.Errorf("left %v and right %v differ", l[at], r[at])
	}
}

// TestFillSilentWithoutSpeaker: nothing pushed is silence, and says so.
func TestFillSilentWithoutSpeaker(t *testing.T) {
	freshRing(t)
	l := []float32{1, 1}
	r := []float32{1, 1}
	if Fill(l, r, Seconds(time.Now())) {
		t.Error("Fill reported audio with no speaker")
	}
	if l[0] != 0 || r[1] != 0 {
		t.Error("Fill left stale samples")
	}
}

// TestClockSnapsOnRestart: a reading far off the line is a stream starting over, not jitter.
func TestClockSnapsOnRestart(t *testing.T) {
	c := NewClock(Rate)
	t0 := time.Now()
	c.Mark(0, t0)
	c.Mark(Rate, t0.Add(time.Second+200*time.Microsecond))
	if d := c.At(0) - Seconds(t0); math.Abs(d) > 50e-6 {
		t.Errorf("jitter moved the line %v s", d)
	}
	c.Mark(Rate, t0.Add(2*time.Second))
	if d := c.At(Rate) - Seconds(t0.Add(2*time.Second)); math.Abs(d) > 1e-6 {
		t.Errorf("a restart was smoothed rather than snapped to: %v s off", d)
	}
}

// TestRestartAfterGap: after the speaker stops and starts again, the reference holds only the new
// audio. Before it, the stretch is empty (the old audio is not placed just ahead of the new one), and
// the new audio's first samples carry nothing of the old through the decimators.
func TestRestartAfterGap(t *testing.T) {
	const (
		rate   = 48000
		period = 768
	)
	freshRing(t)
	stamp := func(from time.Time, n int) time.Time {
		return from.Add(time.Duration((n+1)*period-1) * time.Second / rate)
	}
	loud := make([]byte, period*4)
	for i := 0; i < len(loud); i += 2 {
		binary.LittleEndian.PutUint16(loud[i:], uint16(int16(16000)))
	}
	t0 := time.Now()
	for n := 0; n < 10; n++ {
		Push(loud, rate, stamp(t0, n))
	}
	// Silence, after a gap well past stale.
	t1 := t0.Add(500 * time.Millisecond)
	quiet := make([]byte, period*4)
	for n := 0; n < 10; n++ {
		Push(quiet, rate, stamp(t1, n))
	}

	l := make([]float32, Rate*30/1000)
	r := make([]float32, len(l))
	if Fill(l, r, Seconds(t1)-0.06) {
		t.Error("the stretch before the restart reads as the speaker's: the old audio was kept")
	}
	if !Fill(l, r, played.clock.At(0)-Lead.Seconds()) {
		t.Fatal("Fill found nothing at the start of the new audio")
	}
	for i := range l {
		if math.Abs(float64(l[i])) > 1e-4 || math.Abs(float64(r[i])) > 1e-4 {
			t.Fatalf("sample %d of the new audio is %v/%v, want silence: the old audio carried over", i, l[i], r[i])
		}
	}
}

// pushRamp plays n periods of a ramp, every sample a quarter step above the last, stamped without
// jitter, so
// a read shows where in the audio it landed by the value it starts on.
func pushRamp(t0 time.Time, periods int) {
	const rate, period = 48000, 768
	pcm := make([]byte, period*4)
	for n := range periods {
		for i := range period {
			v := uint16(int16((n*period + i) / 4))
			binary.LittleEndian.PutUint16(pcm[4*i:], v)
			binary.LittleEndian.PutUint16(pcm[4*i+2:], v)
		}
		Push(pcm, rate, t0.Add(time.Duration((n+1)*period-1)*time.Second/rate))
	}
}

// TestFillCarriesOn: reads placed by a jittery clock, a late read among them, each start exactly where
// the last one ended, with nothing skipped or repeated across the boundary.
func TestFillCarriesOn(t *testing.T) {
	freshRing(t)
	t0 := time.Now()
	pushRamp(t0, 120)
	const n = Rate / 50
	l, r := make([]float32, n), make([]float32, n)
	start := Seconds(t0) + 0.2
	var last float32
	for k := range 40 {
		jitter := float64(((k+2)%5)-2) * 300e-6 // none on the first read, which the rest follow
		if k == 20 {
			jitter = 0.006 // a read that came back late
		}
		if !Fill(l, r, start+float64(k*n)/Rate+jitter) {
			t.Fatalf("read %d found nothing", k)
		}
		if k > 0 {
			// Three input samples to an output sample: the ramp rises 3/4 of a step across the boundary.
			step := float64(l[0]-last) * (1 << 15) * 4 / 3
			if math.Abs(step-1) > 0.4 {
				t.Fatalf("read %d starts %.1f samples after the last one ended, want 1", k, step)
			}
		}
		last = l[n-1]
	}
}

// TestFillMovesWhenTheAudioDid: a placement that stays away for moveAfter reads is followed.
func TestFillMovesWhenTheAudioDid(t *testing.T) {
	freshRing(t)
	t0 := time.Now()
	pushRamp(t0, 120)
	const n = Rate / 50
	l, r := make([]float32, n), make([]float32, n)
	start := Seconds(t0) + 0.2
	Fill(l, r, start)
	shift := 0.05 // 50 ms: the capture lost a stretch
	for k := 1; k <= moveAfter; k++ {
		Fill(l, r, start+float64(k*n)/Rate+shift)
	}
	want := math.Round((start+float64(moveAfter*n)/Rate+shift+Lead.Seconds()-played.clock.off)*Rate) + n
	if played.next != want {
		t.Errorf("after %d reads 50 ms away the reference is at %.0f, want %.0f", moveAfter, played.next, want)
	}
}

// freshRing gives the test a reference of its own, and puts the package's back when it ends.
func freshRing(t *testing.T) {
	old := played
	played = &ring{clock: NewClock(Rate)}
	t.Cleanup(func() { played = old })
}

// placed is where the clocks alone would place a read starting at start, as Fill computes it.
func placed(start float64) float64 {
	return math.Round((start + Lead.Seconds() - played.clock.off) * Rate)
}

// TestFillRejoins: a reader whose stream started over is placed by the clocks at once, not carried on
// from before its gap.
func TestFillRejoins(t *testing.T) {
	freshRing(t)
	t0 := time.Now()
	pushRamp(t0, 120)
	const n = Rate / 50
	l, r := make([]float32, n), make([]float32, n)
	start := Seconds(t0) + 0.2
	for k := range 5 {
		Fill(l, r, start+float64(k*n)/Rate)
	}
	Rejoin()
	later := start + 0.3 // the capture lost 200 ms past where it would have been
	Fill(l, r, later)
	if want := placed(later) + n; played.next != want {
		t.Errorf("after Rejoin the read carried on to %.0f, want it placed at %.0f", played.next, want)
	}
}

// TestFillHoldsItsPlace: clocks that place every read a few samples off where the last one ended, the
// same way every time, do not walk the reference after them; only a real move does (moveAfter).
func TestFillHoldsItsPlace(t *testing.T) {
	freshRing(t)
	t0 := time.Now()
	pushRamp(t0, 120)
	const n = Rate / 50
	l, r := make([]float32, n), make([]float32, n)
	start := Seconds(t0) + 0.2
	Fill(l, r, start)
	held := played.next
	for k := 1; k <= 200; k++ {
		Fill(l, r, start+float64(k*n+6)/Rate) // six samples later than carrying on, every read
	}
	if want := held + 200*n; played.next != want {
		t.Errorf("after 200 reads the reference is at %.0f, want it held at %.0f", played.next, want)
	}
}

// TestFillFollowsALastingSmallShift: a shift of a few milliseconds that lasts (a speaker underrun's gap,
// or clocks drifting apart) is followed after moveAfter reads, where a single read that far off is not.
func TestFillFollowsALastingSmallShift(t *testing.T) {
	freshRing(t)
	t0 := time.Now()
	pushRamp(t0, 120)
	const n = Rate / 50
	l, r := make([]float32, n), make([]float32, n)
	start := Seconds(t0) + 0.2
	Fill(l, r, start)
	shift := 0.005 // 5 ms: more than settle, less than a late read's
	Fill(l, r, start+float64(n)/Rate+shift)
	if want := placed(start) + 2*n; played.next != want {
		t.Fatalf("one read 5 ms off moved the reference to %.0f, want it held at %.0f", played.next, want)
	}
	for k := 2; k <= moveAfter+1; k++ {
		Fill(l, r, start+float64(k*n)/Rate+shift)
	}
	if want := placed(start+float64((moveAfter+1)*n)/Rate+shift) + n; played.next != want {
		t.Errorf("after %d reads 5 ms off the reference is at %.0f, want %.0f", moveAfter, played.next, want)
	}
}
