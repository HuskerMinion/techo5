package timer

import (
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

var nextNow = time.Date(2026, 10, 17, 18, 38, 0, 0, time.UTC)

func hold(ts *Timers, id string, c *timer) {
	ts.mu.Lock()
	ts.held[id] = c
	ts.mu.Unlock()
}

func running(name string, left time.Duration) *timer {
	return &timer{name: name, total: left, left: left, at: nextNow, active: true}
}

func paused(name string, left time.Duration) *timer {
	return &timer{name: name, total: time.Hour, left: left, at: nextNow}
}

func wantNext(t *testing.T, ts *Timers, want nextText) {
	t.Helper()
	if got := ts.nextTimer(nextNow); got != want {
		t.Errorf("next timer %+v, want %+v", got, want)
	}
}

func TestNoTimersLeaveTheNextTimerEmpty(t *testing.T) {
	wantNext(t, build(), nextText{})
}

func TestTheNextTimerIsTheRunningOneThatEndsSoonest(t *testing.T) {
	ts := build()
	hold(ts, "pasta", running("Pasta", 10*time.Minute))
	hold(ts, "eggs", running("Eggs", 4*time.Minute))
	wantNext(t, ts, nextText{ends: "2026-10-17T18:42:00Z", label: "Eggs"})

	ts.forget("eggs")
	wantNext(t, ts, nextText{ends: "2026-10-17T18:48:00Z", label: "Pasta"})
}

// A paused timer with less left is not next while another runs: it is not counting towards anything.
func TestARunningTimerComesBeforeAPausedOne(t *testing.T) {
	ts := build()
	hold(ts, "bread", paused("Bread", time.Minute))
	hold(ts, "pasta", running("Pasta", 10*time.Minute))
	wantNext(t, ts, nextText{ends: "2026-10-17T18:48:00Z", label: "Pasta"})
}

func TestWithNoneRunningAPausedTimerHasNoEndAndSaysWhatIsLeft(t *testing.T) {
	ts := build()
	hold(ts, "bread", paused("Bread", 4*time.Minute+5*time.Second))
	hold(ts, "stew", paused("Stew", time.Hour))
	wantNext(t, ts, nextText{label: "Bread (paused, 4:05 left)"})

	// Resumed, it counts down again.
	ts.mu.Lock()
	ts.held["bread"].active = true
	ts.mu.Unlock()
	wantNext(t, ts, nextText{ends: "2026-10-17T18:42:05Z", label: "Bread"})
}

func TestASilentTimerIsNeverTheNextTimer(t *testing.T) {
	ts := build()
	hold(ts, "wait", &timer{name: "Joining", total: time.Minute, left: time.Minute, at: nextNow, active: true, local: true, silent: true, color: led.Color{B: 0xFF}})
	wantNext(t, ts, nextText{})

	hold(ts, "eggs", running("Eggs", 4*time.Minute))
	wantNext(t, ts, nextText{ends: "2026-10-17T18:42:00Z", label: "Eggs"})
}

// A timer of Home Assistant's at zero, its finishing event late, has an end that has gone by: the next
// timer is the one still counting, or none.
func TestATimerAtZeroIsNotTheNextTimer(t *testing.T) {
	ts := build()
	done := running("Eggs", time.Minute)
	done.at = nextNow.Add(-2 * time.Minute)
	hold(ts, "eggs", done)
	wantNext(t, ts, nextText{})

	hold(ts, "pasta", running("Pasta", 10*time.Minute))
	wantNext(t, ts, nextText{ends: "2026-10-17T18:48:00Z", label: "Pasta"})
}

func TestAnUnnamedNextTimerIsCalledTimer(t *testing.T) {
	ts := build()
	hold(ts, "tea", running("", 3*time.Minute))
	wantNext(t, ts, nextText{ends: "2026-10-17T18:41:00Z", label: "Timer"})
}

// Home Assistant re-anchors its own timers on every event; a fraction of a second is not a new end.
func TestTheNextTimersEndIsToTheSecond(t *testing.T) {
	ts := build()
	c := running("Eggs", 4*time.Minute)
	c.at = nextNow.Add(400 * time.Millisecond)
	hold(ts, "eggs", c)
	wantNext(t, ts, nextText{ends: "2026-10-17T18:42:00Z", label: "Eggs"})
}

// What Home Assistant sees, through an event from it as much as a timer set here.
func TestTheNextTimerIsPublishedWithTheTimers(t *testing.T) {
	useTestState(t)
	ts := build()
	ts.Event(started("eggs", 240))
	if ts.next.label.Get() != "eggs" || ts.next.ends.Get() == "" {
		t.Errorf("next timer is %q ending %q, want eggs with an end", ts.next.label.Get(), ts.next.ends.Get())
	}
	ts.Event(esphome.TimerEvent{Type: api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_CANCELLED, TimerID: "eggs"})
	if ts.next.label.Get() != "" || ts.next.ends.Get() != "" {
		t.Errorf("next timer is %q ending %q after the cancel, want none", ts.next.label.Get(), ts.next.ends.Get())
	}
}
