package voice

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// wordsFor makes the words play from start to end after afterWords begins, shortens the waits, and
// counts the hushes.
func wordsFor(t *testing.T, start, end time.Duration) *atomic.Int32 {
	t.Helper()
	cancel, begin, longest := callCancel, wordsStart, wordsLongest
	say, quiet := announcing, hush
	t.Cleanup(func() {
		callCancel, wordsStart, wordsLongest = cancel, begin, longest
		announcing, hush = say, quiet
	})
	callCancel, wordsStart, wordsLongest = 150*time.Millisecond, 300*time.Millisecond, time.Second
	t0 := time.Now()
	var hushed atomic.Int32
	announcing = func() bool {
		if hushed.Load() > 0 {
			return false
		}
		d := time.Since(t0)
		return start >= 0 && d >= start && d < end
	}
	hush = func() { hushed.Add(1) }
	return &hushed
}

// The whole wait comes after the words, however late they start within wordsStart.
func TestACallWaitsAfterLateWords(t *testing.T) {
	wordsFor(t, 250*time.Millisecond, 500*time.Millisecond)
	t0 := time.Now()
	if !afterWords(context.Background()) {
		t.Fatal("the call was dropped")
	}
	if d := time.Since(t0); d < 500*time.Millisecond+150*time.Millisecond {
		t.Errorf("dialed %v after the start, before the words had ended and the wait gone by", d)
	}
}

// No words, no call: nobody heard whom it would call.
func TestACallWithNoWordsIsDropped(t *testing.T) {
	wordsFor(t, -1, 0)
	if afterWords(context.Background()) {
		t.Fatal("a call nobody heard named was dialed")
	}
}

// Calling it off while the words play stops them.
func TestCallingOffStopsTheWords(t *testing.T) {
	hushed := wordsFor(t, 0, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if afterWords(ctx) {
		t.Fatal("a call called off was dialed")
	}
	if hushed.Load() == 0 {
		t.Error("the words went on after the call was called off")
	}
}
