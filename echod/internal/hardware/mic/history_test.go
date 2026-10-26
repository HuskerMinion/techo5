package mic

import (
	"fmt"
	"runtime"
	"slices"
	"testing"
	"time"
)

func historyFrameNumbers(pre []int16) []int16 {
	var out []int16
	for i := 0; i < len(pre); i += FrameSamples {
		out = append(out, pre[i])
	}
	return out
}

func TestListenHistoryJoinsOnce(t *testing.T) {
	for _, d := range []time.Duration{0, History, Confirming} {
		t.Run(d.String(), func(t *testing.T) {
			s := testSource()
			s.mixer = &reusedBuffer{}
			s.broadcast(rawFrame(0, false))
			frames, pre, stop := s.ListenHistory("test", d)
			defer stop()
			s.broadcast(rawFrame(1, false))
			got := append(historyFrameNumbers(pre), (<-frames)[0])
			want := []int16{1, 2}
			if d == 0 {
				want = []int16{2}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("joined frames %v, want %v", got, want)
			}
			stop()
			stop()
			if _, ok := <-frames; ok {
				t.Fatal("unsubscribe did not close channel")
			}
			if len(s.listeners) != 0 {
				t.Fatal("subscription leaked")
			}
		})
	}
}

// Pause the real snapshot on history.mu. Capture must wait for the whole
// subscription/snapshot transaction, so the next frame appears only as live.
func TestListenHistorySerializesCapture(t *testing.T) {
	s := testSource()
	s.mixer = &reusedBuffer{}
	s.broadcast(rawFrame(0, false))
	s.history.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.history.mu.Unlock()
		}
	}()
	type joined struct {
		pre    []int16
		frames <-chan []int16
		stop   func()
	}
	acquired := make(chan joined, 1)
	go func() {
		frames, pre, stop := s.ListenHistory("paused", Confirming)
		acquired <- joined{pre, frames, stop}
	}()
	until := time.Now().Add(time.Second)
	for s.mu.TryLock() {
		s.mu.Unlock()
		if time.Now().After(until) {
			t.Fatal("snapshot did not acquire Source.mu")
		}
		runtime.Gosched()
	}
	captured := make(chan struct{})
	go func() { s.broadcast(rawFrame(1, false)); close(captured) }()
	s.history.mu.Unlock()
	locked = false
	var got joined
	select {
	case got = <-acquired:
	case <-time.After(time.Second):
		t.Fatal("snapshot blocked")
	}
	defer got.stop()
	select {
	case <-captured:
	case <-time.After(time.Second):
		t.Fatal("capture blocked")
	}
	numbers := append(historyFrameNumbers(got.pre), (<-got.frames)[0])
	if !slices.Equal(numbers, []int16{1, 2}) {
		t.Fatalf("joined frames %v, want [1 2]", numbers)
	}
}

func TestListenHistoryConcurrentCapture(t *testing.T) {
	s := testSource()
	s.mixer = &reusedBuffer{}
	for range 100 {
		// Both possible lock orders are valid: the boundary frame is in history
		// or live. It must occur exactly once, and capture's final frame follows it.
		start := make(chan struct{})
		done := make(chan struct{})
		go func() { <-start; s.broadcast(rawFrame(0, false)); close(done) }()
		close(start)
		frames, pre, stop := s.ListenHistory("concurrent", 20*time.Millisecond)
		<-done
		s.broadcast(rawFrame(0, false))
		got := historyFrameNumbers(pre)
		for len(frames) > 0 {
			got = append(got, (<-frames)[0])
		}
		stop()
		for i := 1; i < len(got); i++ {
			if got[i] != got[i-1]+1 {
				t.Fatalf("gap or overlap: %v", got)
			}
		}
	}
	if s.Dropped() != 0 {
		t.Fatal("fixture dropped frames")
	}
}

func BenchmarkHistorySubscription(b *testing.B) {
	for _, d := range []time.Duration{0, History, Confirming} {
		b.Run(fmt.Sprint(d), func(b *testing.B) {
			s := testSource()
			s.history.add(make([]int16, historySamples))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _, stop := s.ListenHistory("bench", d)
				stop()
			}
		})
	}
}

func BenchmarkHistoryBroadcast(b *testing.B) {
	s := testSource()
	s.mixer = Center{}
	raw := rawFrame(0, false)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.broadcast(raw)
	}
}
