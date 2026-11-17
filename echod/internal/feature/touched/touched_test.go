//go:build !dot

package touched

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

var t0 = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// A quick tap, down and up before the loop looks, turns the sensor on and it stays on for the hold.
func TestATapHoldsTheSensorOn(t *testing.T) {
	var s state
	if !s.look(t0, true, false, hold) || !s.on {
		t.Fatal("a tap did not turn the sensor on")
	}
	if s.expire(t0.Add(hold-time.Millisecond), false) {
		t.Error("the sensor went off before the hold ran out")
	}
	if !s.expire(t0.Add(hold), false) || s.on {
		t.Error("the sensor stayed on after the hold")
	}
}

// A finger left on the panel keeps the sensor on however long it stays, and the hold starts as it
// lifts.
func TestAFingerDownKeepsTheSensorOn(t *testing.T) {
	var s state
	s.look(t0, true, true, hold)
	if s.expire(t0.Add(time.Hour), true) {
		t.Fatal("the sensor went off under a finger")
	}
	lift := t0.Add(time.Hour)
	if s.look(lift, false, false, hold) {
		t.Error("lifting the finger changed the sensor")
	}
	if s.expire(lift.Add(hold-time.Millisecond), false) {
		t.Error("the sensor went off before the hold after the lift ran out")
	}
	if !s.expire(lift.Add(hold), false) {
		t.Error("the sensor stayed on after the hold after the lift")
	}
}

// Tapping on keeps the sensor on without it going off in between: each tap starts the hold over, and
// only a tap that turns it on changes it.
func TestTappingOnDoesNotFlap(t *testing.T) {
	var s state
	changes := 0
	for i := range 10 {
		at := t0.Add(time.Duration(i) * (hold - time.Second))
		if s.expire(at, false) {
			t.Fatalf("the sensor went off between taps, at tap %d", i)
		}
		if s.look(at, true, false, hold) {
			changes++
		}
	}
	if changes != 1 {
		t.Errorf("tapping on changed the sensor %d times, want once", changes)
	}
	last := t0.Add(9 * (hold - time.Second))
	if !s.expire(last.Add(hold), false) {
		t.Error("the sensor stayed on after the last tap's hold")
	}
}

// Nothing landed, nothing down: an off sensor stays off, and an off sensor has nothing to expire.
func TestNoTouchLeavesTheSensorOff(t *testing.T) {
	var s state
	if s.look(t0, false, false, hold) || s.on {
		t.Error("a wake with no finger turned the sensor on")
	}
	if s.expire(t0.Add(time.Hour), false) {
		t.Error("an off sensor expired")
	}
}

// fakeScreen is a touchscreen a test lands and lifts fingers on.
type fakeScreen struct {
	mu   sync.Mutex
	down bool
	hook hook.Hook[bool]
}

func (f *fakeScreen) Touched() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.down
}

func (f *fakeScreen) Listen(fn func(bool)) func() { return f.hook.Listen(fn) }

func (f *fakeScreen) finger(down bool) {
	f.mu.Lock()
	f.down = down
	f.mu.Unlock()
	f.hook.Emit(down)
}

// The loop starts the sensor off, turns it on as a finger lands, keeps it on while it is down and
// turns it off once the hold after the lift has run out.
func TestTheLoopWritesTheSensor(t *testing.T) {
	const short = 50 * time.Millisecond
	src := &fakeScreen{}
	sets := make(chan bool, 10)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, src, func(on bool) { sets <- on }, short) }()

	next := func(want bool, what string) {
		t.Helper()
		select {
		case got := <-sets:
			if got != want {
				t.Fatalf("%s: the sensor was set to %v, want %v", what, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: the sensor was not set", what)
		}
	}
	next(false, "start")

	// Listen is called before the first set, so the finger is heard from here on.
	src.finger(true)
	next(true, "finger down")
	time.Sleep(3 * short)
	select {
	case got := <-sets:
		t.Fatalf("the sensor was set to %v under a finger", got)
	default:
	}
	lifted := time.Now()
	src.finger(false)
	next(false, "after the hold")
	if gone := time.Since(lifted); gone < short {
		t.Errorf("the sensor went off %v after the lift, before the hold of %v", gone, short)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("run: %v", err)
	}
	if len(sets) != 0 {
		t.Errorf("the sensor was set %d more times", len(sets))
	}
}
