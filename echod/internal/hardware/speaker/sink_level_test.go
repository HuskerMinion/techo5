package speaker

import (
	"math"
	"slices"
	"sync"
	"testing"
	"time"
)

// levelsTold records the steps a sink with its own volume is told.
type levelsTold struct {
	mu    sync.Mutex
	steps []int
}

func (l *levelsTold) set(step int) {
	l.mu.Lock()
	l.steps = append(l.steps, step)
	l.mu.Unlock()
}

func (l *levelsTold) got() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int(nil), l.steps...)
}

// waitFor waits a little for the steps told to be want.
func (l *levelsTold) waitFor(t *testing.T, want []int) {
	t.Helper()
	for range 100 {
		if slices.Equal(l.got(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("told %v, want %v", l.got(), want)
}

func gainNear(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-6 }

// With its own volume handed to the sink, the media volume goes to the sink and the audio here plays
// at unity: the volume is applied once, not twice (#550). A step below the sink's level is scaled by
// the difference, and none is ever above unity.
func TestASinkWithItsOwnVolumePlaysAtUnity(t *testing.T) {
	for _, c := range []struct {
		step, at int
		want     float32
	}{
		{12, 12, 1}, {VolumeSteps, VolumeSteps, 1}, {1, 1, 1},
		{12, 20, sinkGain(12) / sinkGain(20)}, {20, 12, 1}, {0, 12, 0}, {12, 0, 0},
	} {
		if got := sinkShare(c.step, c.at); !gainNear(got, c.want) {
			t.Errorf("step %d at %d: gain %v, want %v", c.step, c.at, got, c.want)
		}
	}

	p := New()
	p.SetVolume(12)
	sink := &fakeSink{}
	p.SetSink(sink, "test", Rate, Channels)
	if !gainNear(p.Volume(), sinkGain(12)) {
		t.Fatalf("in software: gain %v, want %v", p.Volume(), sinkGain(12))
	}
	told := &levelsTold{}
	p.SetSinkLevel(sink, told.set)
	told.waitFor(t, []int{12})
	if p.Volume() != 1 || p.gainFor(12) != 1 {
		t.Errorf("with the sink's own volume: gain %v, gainFor %v, want unity", p.Volume(), p.gainFor(12))
	}
	// A drop goes at once; the audio stays at unity either way.
	p.SetVolume(5)
	told.waitFor(t, []int{12, 5})
	if p.Volume() != 1 {
		t.Errorf("at step 5: gain %v, want unity", p.Volume())
	}
	// Muted is the sink at 0.
	p.SetVolume(0)
	told.waitFor(t, []int{12, 5, 0})
	if p.Volume() != 0 {
		t.Errorf("muted: gain %v", p.Volume())
	}
	p.SetVolume(5)
	told.waitFor(t, []int{12, 5, 0, 5})

	// Another sink's word changes nothing; nil puts the volume back in software.
	p.SetSinkLevel(&fakeSink{}, nil)
	if p.Volume() != 1 {
		t.Error("another sink took the volume back")
	}
	p.SetSinkLevel(sink, nil)
	if !gainNear(p.Volume(), sinkGain(5)) {
		t.Errorf("back in software: gain %v, want %v", p.Volume(), sinkGain(5))
	}
	p.SetVolume(9)
	time.Sleep(2 * sinkLag)
	if got := told.got(); len(got) != 4 {
		t.Errorf("back in software the sink was still told: %v", got)
	}
	p.SetSink(nil, "", 0, 0)
}

// A ring above the media volume raises the sink's own volume, a moment after the samples scaled for
// it, and turns the music down here by as much; it ending puts both back at once.
func TestARingRaisesTheSinksOwnVolume(t *testing.T) {
	p := New()
	p.SetVolume(10)
	sink := &fakeSink{}
	p.SetSink(sink, "test", Rate, Channels)
	told := &levelsTold{}
	p.SetSinkLevel(sink, told.set)
	told.waitFor(t, []int{10})

	start := time.Now()
	p.sinkRing(true, 20)
	if !gainNear(p.Volume(), sinkGain(10)/sinkGain(20)) || p.gainFor(20) != 1 {
		t.Errorf("ringing: music %v, ring %v; want %v and unity", p.Volume(), p.gainFor(20), sinkGain(10)/sinkGain(20))
	}
	p.sinkRing(true, 20) // every period says the same: told once
	told.waitFor(t, []int{10, 20})
	if waited := time.Since(start); waited < sinkLag {
		t.Errorf("the raise went after %v, before the samples (%v)", waited, sinkLag)
	}
	p.sinkRing(false, 20)
	told.waitFor(t, []int{10, 20, 10})
	if p.Volume() != 1 {
		t.Errorf("after the ring: gain %v, want unity", p.Volume())
	}
	// A ring below the media volume moves nothing.
	p.sinkRing(true, 4)
	time.Sleep(2 * sinkLag)
	if got := told.got(); len(got) != 3 || p.Volume() != 1 {
		t.Errorf("a quieter ring: told %v, gain %v", got, p.Volume())
	}
	p.SetSink(nil, "", 0, 0)
}

// Two steps in quick succession reach the sink in order, the later one last, even when the first was a
// raise held back for sinkLag.
func TestSinkLevelsArriveInOrder(t *testing.T) {
	p := New()
	p.SetVolume(10)
	sink := &fakeSink{}
	p.SetSink(sink, "test", Rate, Channels)
	told := &levelsTold{}
	p.SetSinkLevel(sink, told.set)
	told.waitFor(t, []int{10})
	p.SetVolume(20) // a raise, held back
	p.SetVolume(8)  // a drop, at once: the raise is dropped
	told.waitFor(t, []int{10, 8})
	time.Sleep(2 * sinkLag)
	if got := told.got(); !slices.Equal(got, []int{10, 8}) {
		t.Errorf("told %v, want [10 8]", got)
	}
	p.SetSink(nil, "", 0, 0)
}
