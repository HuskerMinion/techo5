package timer

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A running timer is written down as the moment it goes off, not as what it has left.
//
// A duration left means nothing once the process has stopped: it would mean the same five minutes
// however long the device was off. A finish time means the same thing whatever happened in between.
func TestALocalTimerIsSavedAsWhenItGoesOff(t *testing.T) {
	now := time.Date(2026, 9, 16, 14, 7, 0, 0, time.UTC)
	held := map[string]*timer{
		"local:a":  {name: "Pasta", total: 10 * time.Minute, left: 4 * time.Minute, at: now, active: true, local: true},
		"local:b":  {name: "Bread", total: time.Hour, left: 20 * time.Minute, at: now, local: true}, // paused
		"ha-12345": {name: "Theirs", total: time.Minute, left: time.Minute, at: now, active: true},
	}

	got := localList(held, now)

	// Home Assistant's timer is not ours to remember: it holds them in memory and comes back without
	// them, so one written down here would ring with nobody able to say what for.
	if len(got) != 2 {
		t.Fatalf("saved %d timers, want only the two local ones: %+v", len(got), got)
	}

	running, paused := got[0], got[1]
	if running.ID != "local:a" || paused.ID != "local:b" {
		t.Fatalf("saved in an unexpected order: %+v", got)
	}
	if want := now.Add(4 * time.Minute); !running.Finish.Equal(want) {
		t.Errorf("a running timer finishes at %v, want %v", running.Finish, want)
	}
	if running.Left != 0 {
		t.Errorf("a running timer also saved a duration left (%v); the finish time is the record", running.Left)
	}
	if !paused.Finish.IsZero() {
		t.Errorf("a paused timer saved a finish time (%v); it is not counting down to one", paused.Finish)
	}
	if paused.Left != 20*time.Minute {
		t.Errorf("a paused timer has %v left, want 20m", paused.Left)
	}
}

// A timer that came back is counting down again; one whose moment went by while the device was off
// does not ring.
//
// Not ringing is the deliberate part. A crash loop would otherwise be a device that screams every
// time it boots, and a timer nobody heard is not put right by sounding an hour later.
func TestRestoreBringsBackWhatIsStillRunningAndDoesNotRing(t *testing.T) {
	ts := build()
	now := time.Now()

	ts.Restore(config.Config{Timers: config.Timers{Local: []config.LocalTimer{
		{ID: "local:gone", Name: "Missed", Total: 10 * time.Minute, Finish: now.Add(-2 * time.Hour)},
		{ID: "local:live", Name: "Pasta", Total: 10 * time.Minute, Finish: now.Add(4 * time.Minute)},
		{ID: "local:held", Name: "Bread", Total: time.Hour, Left: 20 * time.Minute},
	}}})

	if _, ringing := ts.RingingName(); ringing {
		t.Error("a timer that finished while the device was off rang at start-up")
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	if _, ok := ts.held["local:gone"]; ok {
		t.Error("a timer whose moment had gone was brought back")
	}
	live, ok := ts.held["local:live"]
	if !ok {
		t.Fatal("a timer still running was not brought back")
	}
	if !live.active || !live.local {
		t.Errorf("the restored timer is active=%v local=%v, want both true", live.active, live.local)
	}
	if left := live.remaining(now); left < 3*time.Minute || left > 5*time.Minute {
		t.Errorf("the restored timer has %v left, want about four minutes", left)
	}
	held, ok := ts.held["local:held"]
	if !ok {
		t.Fatal("a paused timer was not brought back")
	}
	if held.active {
		t.Error("a paused timer came back running")
	}
	if held.left != 20*time.Minute {
		t.Errorf("the paused timer has %v left, want 20m", held.left)
	}
}

// Home Assistant going away takes its own timers and leaves the device's.
//
// A local timer finishes from this clock and is meant to run with Home Assistant away — that is what
// makes it local. Forget used to empty the whole map, so every disconnect threw away the one kind
// that was still counting down to something real.
func TestForgetKeepsTheDevicesOwnTimers(t *testing.T) {
	ts := build()
	now := time.Now()

	ts.mu.Lock()
	ts.held["local:mine"] = &timer{name: "Pasta", total: time.Hour, left: time.Hour, at: now, active: true, local: true}
	ts.held["ha-theirs"] = &timer{name: "Theirs", total: time.Hour, left: time.Hour, at: now, active: true}
	ts.mu.Unlock()

	ts.Forget()

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if _, ok := ts.held["ha-theirs"]; ok {
		t.Error("Home Assistant's timer survived it going away; it is counting down to nothing")
	}
	if _, ok := ts.held["local:mine"]; !ok {
		t.Error("the device's own timer was forgotten because Home Assistant went away")
	}
}
