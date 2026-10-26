package ring

import (
	"math"
	"testing"
	"time"
)

// A rising ring starts riseFloor below full, climbs evenly in dB, and stays at full once the rise is
// over; one that does not rise is full from the start.
func TestRingGain(t *testing.T) {
	from := time.Unix(1000, 0)
	r := &ringer{from: from, rise: time.Minute}
	db := func(g float64) float64 { return 20 * math.Log10(g) }
	if got := db(r.gain(from)); math.Abs(got+riseFloor) > 0.01 {
		t.Errorf("first chime at %.1f dB, want %.1f", got, -riseFloor)
	}
	if got := db(r.gain(from.Add(30 * time.Second))); math.Abs(got+riseFloor/2) > 0.01 {
		t.Errorf("halfway at %.1f dB, want %.1f", got, -riseFloor/2)
	}
	for _, at := range []time.Duration{time.Minute, 10 * time.Minute} {
		if g := r.gain(from.Add(at)); g != 1 {
			t.Errorf("after %v at %v of full", at, g)
		}
	}
	if g := (&ringer{from: from}).gain(from); g != 1 {
		t.Errorf("a ring that does not rise starts at %v of full", g)
	}
}

// The bell plays a rising ring quieter first and louder later.
func TestBellRises(t *testing.T) {
	rm := quietBell(t, time.Second)
	stop := StartRising("alarm", "", nil, 200*time.Millisecond, func() {})
	time.Sleep(300 * time.Millisecond)
	stop()
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if len(rm.gains) < 2 {
		t.Fatalf("%d chimes", len(rm.gains))
	}
	first, last := rm.gains[0], rm.gains[len(rm.gains)-1]
	if first >= 0.1 || last != 1 {
		t.Errorf("first chime at %v of full, last at %v", first, last)
	}
}
