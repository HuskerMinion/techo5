package timer

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// An add-on's timer is the device's own, carrying its owner: the add-on lists and cancels its own and
// no other, is told when it rings and when the ring is stopped, and when anything else cancels it.
func TestAnAddonsTimerIsItsOwn(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })
	var mu sync.Mutex
	var told []string
	ts.Owned.Listen(func(e OwnedEvent) {
		mu.Lock()
		told = append(told, e.Owner+" "+e.Name+" "+e.State)
		mu.Unlock()
	})
	mine := ts.StartOwned("meals", "rice", 10*time.Minute)
	theirs := ts.StartOwned("other", "tea", time.Minute)
	plain := ts.Start("pasta", time.Minute)

	list := ts.OwnedBy("meals", time.Now())
	if len(list) != 1 || list[0].ID != mine || !list[0].Local || list[0].Owner != "meals" {
		t.Fatalf("its timers: %+v", list)
	}
	if ts.CancelOwned("meals", theirs) || ts.CancelOwned("meals", plain) {
		t.Error("an add-on canceled a timer not its own")
	}
	if len(ts.OwnedBy("", time.Now())) != 0 {
		t.Error("the empty owner lists timers")
	}
	// The screen, or Home Assistant, canceling it tells the add-on; the add-on canceling it does not.
	if !ts.Cancel(theirs) || !ts.CancelOwned("meals", mine) {
		t.Fatal("cancel")
	}
	mine = ts.StartOwned("meals", "rice", 10*time.Minute)
	ts.mu.Lock()
	now := ts.held[mine].at
	ts.mu.Unlock()
	ts.ripe(now.Add(10 * time.Minute))
	if !ts.Ringing() {
		t.Fatal("an add-on's timer did not ring")
	}
	ts.Stop()
	waitQuiet(t, ts)
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"other tea canceled", "meals rice ringing", "meals rice stopped"}; !slices.Equal(told, want) {
		t.Errorf("told %q, want %q", told, want)
	}
}

// Removing an add-on cancels its timers, and stops a ring that was its timer's alone.
func TestRemovingAnAddonCancelsItsTimers(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })
	a := ts.StartOwned("meals", "rice", time.Minute)
	ts.StartOwned("meals", "beans", time.Hour)
	ts.Start("pasta", time.Hour)
	ts.mu.Lock()
	now := ts.held[a].at
	ts.mu.Unlock()
	ts.ripe(now.Add(time.Minute))
	if !ts.Ringing() {
		t.Fatal("not ringing")
	}
	if n := ts.CancelAllOwned("meals"); n != 1 {
		t.Errorf("canceled %d, want the one left", n)
	}
	waitQuiet(t, ts)
	if list := ts.List(time.Now()); len(list) != 1 || list[0].Name != "pasta" {
		t.Errorf("left %+v", list)
	}
}

// Its owner is saved with it, and comes back with it after a restart.
func TestAnAddonsTimerIsSavedWithItsOwner(t *testing.T) {
	now := time.Now()
	held := map[string]*timer{"local:a": {name: "Rice", total: time.Minute, left: time.Minute, at: now, active: true, local: true, owner: "meals"}}
	if got := localList(held, now); len(got) != 1 || got[0].Owner != "meals" {
		t.Fatalf("saved %+v", got)
	}
	ts := build()
	ts.Restore(config.Config{Timers: config.Timers{Local: []config.LocalTimer{
		{ID: "local:a", Name: "Rice", Total: time.Minute, Finish: now.Add(time.Minute), Owner: "meals"}}}})
	if list := ts.OwnedBy("meals", now); len(list) != 1 {
		t.Errorf("restored %+v", ts.List(now))
	}
}

// waitQuiet waits for a ring to end: the bell tells the timers so on a goroutine of its own.
func waitQuiet(t *testing.T, ts *Timers) {
	t.Helper()
	for range 400 {
		if !ts.Ringing() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("still ringing")
}
