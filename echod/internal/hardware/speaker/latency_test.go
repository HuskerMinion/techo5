package speaker

import (
	"testing"
	"time"
)

// Before the card has said anything, a frame is taken to wait behind a full ring.
func TestLatencyIsAFullRingUntilTheCardSays(t *testing.T) {
	p := &Player{}
	if got := p.Latency(); got != DefaultLatency {
		t.Fatalf("latency %v, want %v", got, DefaultLatency)
	}
}

// Once it has, it is what the card holds plus the path after it, and nothing to do with the margin
// the driver waits before calling the room quiet.
func TestLatencyIsWhatTheCardHolds(t *testing.T) {
	p := &Player{}
	p.ring.Store(&ringReading{frames: 2880, at: time.Now()})
	want := 60*time.Millisecond + OutputExtra
	if got := p.Latency(); got > want || got < want-5*time.Millisecond {
		t.Fatalf("latency %v, want just under %v", got, want)
	}

	p.ring.Store(&ringReading{frames: 0, at: time.Now()})
	if got := p.Latency(); got != OutputExtra {
		t.Fatalf("latency of an empty ring %v, want %v", got, OutputExtra)
	}
}

// The reading is taken in the write loop and used later from another goroutine: what the card has
// played since comes off, so an old reading does not put the anchor early.
func TestLatencyCountsDownFromTheReading(t *testing.T) {
	p := &Player{}
	p.ring.Store(&ringReading{frames: 2880, at: time.Now().Add(-16 * time.Millisecond)})
	want := 44*time.Millisecond + OutputExtra
	if got := p.Latency(); got > want || got < want-5*time.Millisecond {
		t.Fatalf("latency %v from a reading 16 ms old, want just under %v", got, want)
	}

	// A reading older than the ring it describes says the ring has run dry, not a negative wait.
	p.ring.Store(&ringReading{frames: 2880, at: time.Now().Add(-time.Second)})
	if got := p.Latency(); got != OutputExtra {
		t.Fatalf("latency %v from a reading a second old, want %v", got, OutputExtra)
	}
}

// The frame and its latency come from one reading, so a reader never pairs the count from one write
// with the card's word after another.
func TestPositionPairsTheCountWithItsReading(t *testing.T) {
	p := &Player{}
	p.written.Store(9 * period) // the loop has moved on: the next reading is not in yet
	p.ring.Store(&ringReading{written: 8 * period, frames: 2880, at: time.Now()})
	w, l := p.Position()
	if w != 8*period {
		t.Fatalf("written %d, want the reading's %d", w, 8*period)
	}
	if want := 60*time.Millisecond + OutputExtra; l > want || l < want-5*time.Millisecond {
		t.Fatalf("latency %v, want just under %v", l, want)
	}

	p.ring.Store(nil)
	if w, l := p.Position(); w != 9*period || l != DefaultLatency {
		t.Fatalf("no reading: %d, %v, want the live count %d and %v", w, l, 9*period, DefaultLatency)
	}
}

// A card that stops answering goes back to the full ring rather than keeping a stale reading.
func TestLatencyForgetsACardThatStopsAnswering(t *testing.T) {
	p := &Player{}
	p.ring.Store(&ringReading{frames: 1000, at: time.Now()})
	p.ring.Store(nil)
	if got := p.Latency(); got != DefaultLatency {
		t.Fatalf("latency %v, want %v", got, DefaultLatency)
	}
}

func TestDefaultLatencyIsNotTheQuietMargin(t *testing.T) {
	if DefaultLatency >= HardwareTail {
		t.Fatalf("default latency %v is not below the quiet margin %v", DefaultLatency, HardwareTail)
	}
}
