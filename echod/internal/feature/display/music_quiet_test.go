//go:build !dot

package display

import (
	"testing"
	"time"
)

// The half minute runs from the pause, not from the track's start: still up at 29 s, gone at 31 s,
// and playing again starts it over (#150).
func TestTheQuietTimeRunsFromThePause(t *testing.T) {
	var q quietTimer
	now := time.Unix(1_790_000_000, 0)

	if q.over(now.Add(time.Hour), true, true) {
		t.Fatal("music that is playing counted as quiet")
	}
	paused := now.Add(2 * time.Hour)
	if q.over(paused, true, false) {
		t.Fatal("the page went the moment the music was paused")
	}
	if q.over(paused.Add(29*time.Second), true, false) {
		t.Error("the page went before the half minute was up")
	}
	if !q.over(paused.Add(31*time.Second), true, false) {
		t.Error("the page stayed past the half minute")
	}
	if q.over(paused.Add(40*time.Second), true, true) {
		t.Error("playing again did not start the time over")
	}
	if q.over(paused.Add(41*time.Second), true, false) {
		t.Error("a second pause took the page at once")
	}
}

// Nothing wanting the screen clears the time, so the next music starts with its own.
func TestNothingPlayingClearsTheQuietTime(t *testing.T) {
	var q quietTimer
	now := time.Unix(1_790_000_000, 0)
	q.over(now, true, false)
	q.over(now.Add(10*time.Second), false, false)
	if q.over(now.Add(31*time.Second), true, false) {
		t.Error("the time carried over from music that had gone")
	}
}

// A page brought back by hand while paused gets the half minute again from then; while the music
// plays there is no time to restart.
func TestAPageBroughtBackByHandGetsItsOwnHalfMinute(t *testing.T) {
	var q quietTimer
	now := time.Unix(1_790_000_000, 0)
	q.over(now, true, false)
	if !q.over(now.Add(31*time.Second), true, false) {
		t.Fatal("the page stayed past the half minute")
	}
	q.reopened(now.Add(60 * time.Second))
	if q.over(now.Add(89*time.Second), true, false) {
		t.Error("the page brought back by hand went before its half minute")
	}
	if !q.over(now.Add(91*time.Second), true, false) {
		t.Error("the page brought back by hand stayed past its half minute")
	}

	var playing quietTimer
	playing.over(now, true, true)
	playing.reopened(now)
	if !playing.since.IsZero() {
		t.Error("a reopen while playing started a quiet time")
	}
}
