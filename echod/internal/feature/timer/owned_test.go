package timer

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// told records what the add-ons are told of their timers.
func told(ts *Timers) func() []string {
	var mu sync.Mutex
	var got []string
	ts.Owned.Listen(func(e OwnedEvent) {
		mu.Lock()
		s := e.Owner + " " + e.Name + " " + e.State
		if e.Quiet {
			s += " quietly"
		}
		got = append(got, s)
		mu.Unlock()
	})
	return func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(got) }
}

// An add-on's timer is the device's own, carrying its owner: the add-on lists and cancels its own and
// no other, is told when it rings and when the ring is stopped, and when anything else cancels it.
func TestAnAddonsTimerIsItsOwn(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })
	saw := told(ts)
	mine := ts.StartOwned("meals", "Meals", "rice", 10*time.Minute)
	theirs := ts.StartOwned("other", "Other", "tea", time.Minute)
	plain := ts.Start("pasta", time.Minute)

	list := ts.OwnedBy("meals", time.Now())
	if len(list) != 1 || list[0].ID != mine || !list[0].Local || list[0].Owner != "meals" || list[0].OwnName != "rice" {
		t.Fatalf("its timers: %+v", list)
	}
	if ts.CancelOwned("meals", theirs) || ts.CancelOwned("meals", plain) {
		t.Error("an add-on canceled a timer not its own")
	}
	if len(ts.OwnedBy("", time.Now())) != 0 {
		t.Error("the empty owner lists timers")
	}
	if !ts.Cancel(theirs) || !ts.CancelOwned("meals", mine) {
		t.Fatal("cancel")
	}
	mine = ts.StartOwned("meals", "Meals", "rice", 10*time.Minute)
	ts.mu.Lock()
	now := ts.held[mine].at
	ts.mu.Unlock()
	ts.ripe(now.Add(10 * time.Minute))
	if !ts.Ringing() {
		t.Fatal("an add-on's timer did not ring")
	}
	ts.Stop()
	waitQuiet(t, ts)
	if want := []string{"other tea canceled", "meals rice ringing", "meals rice stopped"}; !slices.Equal(saw(), want) {
		t.Errorf("told %q, want %q", saw(), want)
	}
}

// Wherever timers show, an add-on's says whose it is.
func TestAnAddonsTimerIsShownAsItsOwn(t *testing.T) {
	ts := build()
	ts.StartOwned("meals", "Meals", "rice", time.Minute)
	ts.StartOwned("meals", "Meals", "", time.Hour)
	var names []string
	for _, c := range ts.List(time.Now()) {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, []string{"Meals: rice", "Meals"}) {
		t.Errorf("shown as %q", names)
	}
}

// Removing an add-on cancels its timers, and stops a ring that was its timer's alone.
func TestRemovingAnAddonCancelsItsTimers(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })
	a := ts.StartOwned("meals", "Meals", "rice", time.Minute)
	ts.StartOwned("meals", "Meals", "beans", time.Hour)
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

// An add-on's timers are never written down: setting them costs the flash nothing.
func TestAnAddonsTimerIsNotSaved(t *testing.T) {
	now := time.Now()
	held := map[string]*timer{
		"local:a": {name: "Meals: rice", total: time.Minute, left: time.Minute, at: now, active: true, local: true, owner: "meals"},
		"local:b": {name: "Pasta", total: time.Minute, left: time.Minute, at: now, active: true, local: true},
	}
	if got := localList(held, now); len(got) != 1 || got[0].ID != "local:b" {
		t.Fatalf("saved %+v", got)
	}
}

// In quiet hours an add-on's timer runs out without a sound, and the add-on is told so.
func TestAnAddonsTimerIsSilentInQuietHours(t *testing.T) {
	was := quiet
	quiet = func() bool { return true }
	t.Cleanup(func() { quiet = was })
	ts := build()
	t.Cleanup(func() { ts.Stop() })
	saw := told(ts)
	id := ts.StartOwned("meals", "Meals", "rice", time.Minute)
	ts.Start("pasta", 2*time.Minute)
	ts.mu.Lock()
	now := ts.held[id].at
	ts.mu.Unlock()
	ts.ripe(now.Add(time.Minute))
	if ts.Ringing() {
		t.Error("an add-on's timer rang in quiet hours")
	}
	if got := saw(); !slices.Equal(got, []string{"meals rice ringing quietly"}) {
		t.Errorf("told %q", got)
	}
	// The device's own still rings, as an alarm does: somebody set it for that time.
	ts.ripe(now.Add(3 * time.Minute))
	if !ts.Ringing() {
		t.Error("the device's own timer did not ring in quiet hours")
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
