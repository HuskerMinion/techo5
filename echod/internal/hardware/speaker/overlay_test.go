package speaker

import (
	"math"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Mixing is what makes a beep during a reply immediate rather than arriving after it: what is queued
// is a reply's cushion, or when it came as a file, all of it.
func TestMixSumsIntoWhatIsQueued(t *testing.T) {
	got := mix([]int16{100, 200, 300}, []int16{10, 20, 30})

	if want := []int16{110, 220, 330}; !slices.Equal(got, want) {
		t.Errorf("mix = %v, want %v", got, want)
	}
}

// A tone longer than what is queued keeps sounding past it.
func TestMixExtendsPastTheQueue(t *testing.T) {
	got := mix([]int16{100}, []int16{10, 20, 30})

	if want := []int16{110, 20, 30}; !slices.Equal(got, want) {
		t.Errorf("mix = %v, want %v", got, want)
	}
}

// Two loud things at once must not wrap, which would replace the beep with a crack.
func TestMixClampsInsteadOfWrapping(t *testing.T) {
	got := mix([]int16{math.MaxInt16 - 10, math.MinInt16 + 10}, []int16{1000, -1000})

	if want := []int16{math.MaxInt16, math.MinInt16}; !slices.Equal(got, want) {
		t.Errorf("mix = %v, want %v", got, want)
	}
}

func TestMixIntoNothing(t *testing.T) {
	got := mix(nil, []int16{10, 20})

	if want := []int16{10, 20}; !slices.Equal(got, want) {
		t.Errorf("mix = %v, want %v", got, want)
	}
}

// A tone mixed into the queue is reported as sounding until the queue has played past it, and not
// after, so a call that has fallen behind can drop its own frames rather than drain the tone away.
func TestOverlaying(t *testing.T) {
	p := &Player{}
	p.voice, _ = NewResampler(config.DefaultResampling) // Drain resets it
	if p.Overlaying() {
		t.Fatal("overlaying with nothing queued")
	}
	tone := make([]int16, 3*period*Channels)
	for i := range tone {
		tone[i] = 1000
	}
	p.mu.Lock()
	p.pending = mix(p.pending, tone)
	p.overlaid(len(tone))
	p.mu.Unlock()

	buf := make([]byte, period*Channels*2)
	for i := range 3 {
		if !p.Overlaying() {
			t.Fatalf("not overlaying with %d periods of the tone left", 3-i)
		}
		p.fill(buf)
	}
	if p.Overlaying() {
		t.Error("still overlaying after the tone played out")
	}

	p.mu.Lock()
	p.pending = mix(p.pending, tone)
	p.overlaid(len(tone))
	p.mu.Unlock()
	p.Drain()
	if p.Overlaying() {
		t.Error("still overlaying after a drain")
	}
}
