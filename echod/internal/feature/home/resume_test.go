package home

import (
	"testing"
	"time"
)

// What may be put back on and what may not. A device that restarts what somebody just stopped is
// worse than one that never restarts anything, so the cases that must do nothing are the ones worth
// holding: only the stream that is playing, only a station this device asked for, only so often.
func TestWhatIsWorthPuttingBackOn(t *testing.T) {
	for _, c := range []struct {
		what    string
		set     func(f *Feature)
		ended   string
		wantTry bool
	}{
		{
			what:    "the stream that is playing, after a station was asked for",
			set:     func(f *Feature) { f.url, f.asked, f.askedAt = "http://p/1", "KXYZ", time.Now() },
			ended:   "http://p/1",
			wantTry: true,
		},
		{
			what:  "a stream that is no longer the one playing",
			set:   func(f *Feature) { f.url, f.asked, f.askedAt = "http://p/2", "KXYZ", time.Now() },
			ended: "http://p/1",
		},
		{
			what:  "a stream nobody asked this device for",
			set:   func(f *Feature) { f.url = "http://p/1" },
			ended: "http://p/1",
		},
		{
			what: "a station asked for so long ago that this is something else",
			set: func(f *Feature) {
				f.url, f.asked, f.askedAt = "http://p/1", "KXYZ", time.Now().Add(-askedFor-time.Minute)
			},
			ended: "http://p/1",
		},
		{
			what: "a stream that has already been put back on too many times",
			set: func(f *Feature) {
				f.url, f.asked, f.askedAt = "http://p/1", "KXYZ", time.Now()
				f.resumed, f.resumedAt = resumeTries, time.Now()
			},
			ended: "http://p/1",
		},
	} {
		f := &Feature{}
		c.set(f)
		f.ended(c.ended)

		f.mu.Lock()
		tried := f.resumed > 0 && f.resumedAt.After(time.Time{})
		if c.set != nil && f.resumed == resumeTries && !c.wantTry {
			tried = false // the run it started with, not a try this call made
		}
		f.mu.Unlock()

		if tried != c.wantTry {
			t.Errorf("%s: put back = %t, want %t", c.what, tried, c.wantTry)
		}
	}
}

// The run of tries is forgotten once a stream has stayed up for a while, so an evening of listening
// is not ended by five drops spread across it.
func TestTheRunOfTriesIsForgotten(t *testing.T) {
	f := &Feature{}
	f.url, f.asked, f.askedAt = "http://p/1", "KXYZ", time.Now()
	f.resumed, f.resumedAt = resumeTries, time.Now().Add(-resumeWindow-time.Minute)

	f.ended("http://p/1")

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resumed != 1 {
		t.Errorf("after the window passed the count is %d, expected a fresh first try", f.resumed)
	}
}
