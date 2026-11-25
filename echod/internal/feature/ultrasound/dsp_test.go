package ultrasound

import (
	"encoding/binary"
	"math"
	"math/rand"
	"testing"
	"time"
)

const testRate = 96000

// room makes seconds of three microphones at 96 kHz, S32 left-aligned as the Dot 5's converter gives
// them: the tone at hz straight off the speaker at -30 dBFS, music below 18 kHz at -12 dBFS, noise at
// -70 dBFS, and where moving, an echo at -60 dBFS shifted by doppler Hz.
func room(hz int, seconds float64, doppler float64, seed int64) []byte {
	const ch = 6
	n := int(seconds * testRate)
	out := make([]byte, n*ch*4)
	rng := rand.New(rand.NewSource(seed))
	tone := math.Pow(10, -30.0/20)
	echo := math.Pow(10, -60.0/20)
	music := math.Pow(10, -12.0/20)
	noise := math.Pow(10, -70.0/20)
	for i := range n {
		t := float64(i) / testRate
		for m := range 3 {
			v := tone * math.Cos(2*math.Pi*float64(hz)*t+float64(m))
			v += music * (0.5*math.Sin(2*math.Pi*220*t) + 0.3*math.Sin(2*math.Pi*3100*t) + 0.2*math.Sin(2*math.Pi*16000*t))
			if doppler != 0 {
				v += echo * math.Cos(2*math.Pi*(float64(hz)+doppler)*t+2*float64(m))
			}
			v += noise * rng.NormFloat64()
			binary.LittleEndian.PutUint32(out[(i*ch+m)*4:], uint32(int32(v*(1<<31-1))))
		}
	}
	return out
}

func readings(t *testing.T, hz int, pcm []byte) []reading {
	t.Helper()
	l := newListener(hz, testRate, 3)
	if l == nil {
		t.Fatalf("no listener for %d Hz", hz)
	}
	var out []reading
	l.feed(pcm, 6, 4, 1<<31, func(r reading) { out = append(out, r) })
	return out
}

// A still room hears the tone well and sees nothing in the motion band, music playing or not; an echo
// a couple of hundred hertz off the tone, 30 dB under it, stands well clear of the noise.
func TestTheBasebandTellsMovementFromAStillRoom(t *testing.T) {
	still := readings(t, 32000, room(32000, 4, 0, 1))
	moving := readings(t, 32000, room(32000, 4, 180, 2))
	if len(still) < 20 || len(moving) < 20 {
		t.Fatalf("%d and %d readings out of four seconds", len(still), len(moving))
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, r := range moving {
		lo = min(lo, r.motion)
	}
	for _, r := range still {
		hi = max(hi, r.motion)
	}
	t.Logf("still room up to %.1f dB, moving from %.1f dB; carrier %.1f dB", hi, lo, still[0].carrier)
	for _, r := range still {
		if r.carrier < minCarrier {
			t.Fatalf("the tone is heard only %.1f dB over the noise", r.carrier)
		}
		if r.motion > thresholdFor(100) {
			t.Fatalf("a still room reads %.1f dB of motion", r.motion)
		}
	}
	for _, r := range moving {
		if r.motion < thresholdFor(1) {
			t.Fatalf("an echo moving at 180 Hz reads only %.1f dB", r.motion)
		}
	}
}

// Without the tone there is nothing to measure, and that is said rather than taken for a still room.
func TestNoToneIsNotAStillRoom(t *testing.T) {
	for _, r := range readings(t, 34000, room(32000, 3, 0, 3)) {
		if r.carrier >= minCarrier {
			t.Fatalf("a tone 2 kHz off is heard %.1f dB over the noise", r.carrier)
		}
	}
}

func TestTheDetectorHoldsAndLetsGo(t *testing.T) {
	d := detector{threshold: thresholdFor(50)}
	now := time.Unix(0, 0)
	still := reading{carrier: 40, motion: 0.5}
	move := reading{carrier: 40, motion: 12}
	deaf := reading{carrier: 5, motion: 20}
	for range 10 {
		if d.take(still, now) {
			t.Fatal("motion in a still room")
		}
		now = now.Add(64 * time.Millisecond)
	}
	// Two blocks, each taken: take counts as it reads, so both calls matter.
	first := d.take(move, now)
	second := d.take(move, now)
	if first || second {
		t.Fatal("two blocks of movement counted")
	}
	if !d.take(move, now) {
		t.Fatal("three blocks of movement did not")
	}
	for range ofBlocks { // the movement leaves the window
		d.take(still, now)
	}
	if !d.take(still, now.Add(hold-time.Second)) {
		t.Fatal("motion let go inside the hold")
	}
	if d.take(still, now.Add(hold+time.Second)) {
		t.Fatal("motion held past the hold")
	}
	for range 5 {
		if d.take(deaf, now.Add(2*hold)) {
			t.Fatal("a tone not heard counted as movement")
		}
	}
}

func TestThresholdsGoDownWithSensitivity(t *testing.T) {
	if !(thresholdFor(1) > thresholdFor(50) && thresholdFor(50) > thresholdFor(100) && thresholdFor(100) > 1) {
		t.Errorf("thresholds %.1f, %.1f, %.1f", thresholdFor(1), thresholdFor(50), thresholdFor(100))
	}
}
