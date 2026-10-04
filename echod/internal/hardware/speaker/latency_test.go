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
	p.inRing.Store(2880 + 1)
	want := 60*time.Millisecond + OutputExtra
	if got := p.Latency(); got != want {
		t.Fatalf("latency %v, want %v", got, want)
	}

	p.inRing.Store(0 + 1)
	if got := p.Latency(); got != OutputExtra {
		t.Fatalf("latency of an empty ring %v, want %v", got, OutputExtra)
	}
}

// A card that stops answering goes back to the full ring rather than keeping a stale reading.
func TestLatencyForgetsACardThatStopsAnswering(t *testing.T) {
	p := &Player{}
	p.inRing.Store(1000 + 1)
	p.inRing.Store(0)
	if got := p.Latency(); got != DefaultLatency {
		t.Fatalf("latency %v, want %v", got, DefaultLatency)
	}
}

func TestDefaultLatencyIsNotTheQuietMargin(t *testing.T) {
	if DefaultLatency >= HardwareTail {
		t.Fatalf("default latency %v is not below the quiet margin %v", DefaultLatency, HardwareTail)
	}
}
