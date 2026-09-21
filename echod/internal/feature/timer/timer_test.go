package timer

import (
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

func lit(frame []led.Color) int {
	n := 0
	for _, c := range frame {
		if c != (led.Color{}) {
			n++
		}
	}
	return n
}

func TestTheRingEmptiesAsATimerRunsDown(t *testing.T) {
	total := time.Minute
	for _, c := range []struct {
		left time.Duration
		want int
	}{
		{time.Minute, 12},
		{30 * time.Second, 6},
		{5 * time.Second, 1},
		{0, 0},
	} {
		if got := lit(frame(c.left, total)); got != c.want {
			t.Errorf("%s left of %s: %d segments, want %d", c.left, total, got, c.want)
		}
	}
}

func TestTheLeadingSegmentCarriesThePartOfItStillToRun(t *testing.T) {
	f := frame(32*time.Second, time.Minute)

	if f[6] == (led.Color{}) || f[6] == countdownColor {
		t.Errorf("the leading segment is %v, want it dimmed between black and %v", f[6], countdownColor)
	}
	if f[5] != countdownColor {
		t.Errorf("the segment behind it is %v, want %v", f[5], countdownColor)
	}
	if f[7] != (led.Color{}) {
		t.Errorf("the segment ahead of it is %v, want black", f[7])
	}
}

// A total of zero would divide by it, and a timer that has already finished is drawn as a full ring
// rather than as whatever that arithmetic produced.
func TestATimerWithNoTotalDoesNotDivideByIt(t *testing.T) {
	if got := lit(frame(time.Second, 0)); got != 12 {
		t.Errorf("%d segments, want 12", got)
	}
}

func TestARunningTimerCountsDownWithoutBeingTold(t *testing.T) {
	c := &timer{total: time.Minute, left: time.Minute, at: time.Now().Add(-20 * time.Second), active: true}

	if got := c.remaining(time.Now()); got < 39*time.Second || got > 40*time.Second {
		t.Errorf("%s left, want about 40s", got)
	}
}

func TestAPausedTimerHoldsWhereItWasStopped(t *testing.T) {
	c := &timer{total: time.Minute, left: 40 * time.Second, at: time.Now().Add(-20 * time.Second)}

	if got := c.remaining(time.Now()); got != 40*time.Second {
		t.Errorf("%s left, want 40s", got)
	}
}

func TestARunningTimerNeverGoesPastZero(t *testing.T) {
	c := &timer{total: time.Minute, left: time.Minute, at: time.Now().Add(-2 * time.Minute), active: true}

	if got := c.remaining(time.Now()); got != 0 {
		t.Errorf("%s left, want 0", got)
	}
}

func TestTheSoonestRunningTimerIsTheOneShown(t *testing.T) {
	now := time.Now()
	ts := build()
	ts.held = map[string]*timer{
		"long":  {name: "pasta", left: 10 * time.Minute, at: now, active: true},
		"short": {name: "eggs", left: 2 * time.Minute, at: now, active: true},
	}

	if got := ts.soonest(now); got == nil || got.name != "eggs" {
		t.Errorf("showing %v, want eggs", got)
	}
}

// A paused timer is not counting, so it is not what the ring is counting down.
func TestAPausedTimerIsNotShownEvenWhenItIsTheSoonest(t *testing.T) {
	now := time.Now()
	ts := build()
	ts.held = map[string]*timer{
		"running": {name: "pasta", left: 10 * time.Minute, at: now, active: true},
		"paused":  {name: "eggs", left: 2 * time.Minute, at: now},
	}

	if got := ts.soonest(now); got == nil || got.name != "pasta" {
		t.Errorf("showing %v, want pasta", got)
	}

	ts.held = map[string]*timer{"paused": {name: "eggs", left: 2 * time.Minute, at: now}}
	if got := ts.soonest(now); got != nil {
		t.Errorf("showing %v, want nothing", got)
	}
}

func TestACancelledTimerIsForgotten(t *testing.T) {
	ts := build()
	ts.Event(started("kettle", 180))

	if len(ts.held) != 1 {
		t.Fatalf("holding %d timers, want 1", len(ts.held))
	}

	ts.Event(esphome.TimerEvent{
		Type:    api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_CANCELLED,
		TimerID: "kettle",
	})
	if len(ts.held) != 0 {
		t.Errorf("holding %d timers, want none", len(ts.held))
	}
}

func TestAFinishedTimerRingsUntilItIsStopped(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })

	ts.Event(started("kettle", 180))
	ts.Event(esphome.TimerEvent{
		Type:    api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED,
		TimerID: "kettle",
		Name:    "kettle",
	})

	if !ts.Ringing() {
		t.Fatal("not ringing")
	}
	if len(ts.held) != 0 {
		t.Errorf("holding %d timers, want none: a finished timer is gone at both ends", len(ts.held))
	}
	if !ts.Stop() {
		t.Error("Stop reported nothing to stop")
	}

	for range 100 {
		if !ts.Ringing() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("still ringing after being stopped")
}

func TestWhatIsCountingDownIsNamedSoonestFirst(t *testing.T) {
	ts := build()
	ts.Event(started("pasta", 600))
	ts.Event(started("eggs", 240))

	if got := ts.names.Get(); got != "eggs, pasta" {
		t.Errorf("naming %q, want %q", got, "eggs, pasta")
	}

	ts.Event(esphome.TimerEvent{
		Type:    api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_CANCELLED,
		TimerID: "eggs",
	})
	if got := ts.names.Get(); got != "pasta" {
		t.Errorf("naming %q, want %q", got, "pasta")
	}
}

func TestAnUnnamedTimerStillHasSomethingToCallIt(t *testing.T) {
	ts := build()
	e := started("kettle", 180)
	e.Name = ""
	ts.Event(e)

	if got := ts.names.Get(); got != "Timer" {
		t.Errorf("naming %q, want %q", got, "Timer")
	}
}

func TestStopSaysWhenThereWasNothingRinging(t *testing.T) {
	ts := build()
	ts.Event(started("kettle", 180))

	if ts.Stop() {
		t.Error("Stop claimed to have silenced a timer that was still running")
	}
}

// Forgetting is for a Home Assistant that went away, which has nothing to do with a timer already
// sounding in the room.
func TestForgettingLeavesARingingTimerAlone(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })

	ts.Event(started("pasta", 600))
	ts.Event(esphome.TimerEvent{
		Type:    api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED,
		TimerID: "kettle",
		Name:    "kettle",
	})
	ts.Forget()

	if len(ts.held) != 0 {
		t.Errorf("holding %d timers, want none", len(ts.held))
	}
	if !ts.Ringing() {
		t.Error("stopped ringing")
	}
}

func started(id string, seconds uint32) esphome.TimerEvent {
	return esphome.TimerEvent{
		Type:         api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_STARTED,
		TimerID:      id,
		Name:         id,
		TotalSeconds: seconds,
		SecondsLeft:  seconds,
		IsActive:     true,
	}
}

// A timer set on the device is the same timer from here on: it counts down, it is listed, and it
// rings from this clock rather than waiting to be told.
func TestATimerSetHereCountsDownAndRings(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })

	id := ts.Start("pasta", 10*time.Minute)
	if id == "" {
		t.Fatal("no timer started")
	}
	now := time.Now()
	list := ts.List(now.Add(4 * time.Minute))
	if len(list) != 1 {
		t.Fatalf("listing %d timers, want 1", len(list))
	}
	if !list[0].Local {
		t.Error("a timer set here is not marked as the device's own, so the screen cannot offer to stop it")
	}
	if got := list[0].Left.Round(time.Second); got != 6*time.Minute {
		t.Errorf("%v left after four minutes of ten, want 6m", got)
	}

	ts.ripe(now.Add(9 * time.Minute))
	if ts.Ringing() {
		t.Fatal("ringing with a minute still to run")
	}
	ts.ripe(now.Add(10 * time.Minute))
	if !ts.Ringing() {
		t.Fatal("not ringing after it ran out")
	}
	if len(ts.held) != 0 {
		t.Errorf("holding %d timers, want none: a finished one is gone", len(ts.held))
	}
}

// Cancel is for the device's own timers. Home Assistant's are cancelled where they were set, and it
// says so itself, so a tap here must not make the two disagree.
func TestOnlyTheDevicesOwnTimersAreCancelledHere(t *testing.T) {
	ts := build()
	ts.Event(started("kettle", 180))
	id := ts.Start("pasta", time.Minute)

	if ts.Cancel("kettle") {
		t.Error("cancelled a Home Assistant timer from the device")
	}
	if len(ts.held) != 2 {
		t.Fatalf("holding %d timers, want both", len(ts.held))
	}
	if !ts.Cancel(id) {
		t.Error("did not cancel the device's own timer")
	}
	if len(ts.held) != 1 {
		t.Errorf("holding %d timers, want Home Assistant's alone", len(ts.held))
	}
}

// One alarm covers however many timers went off, whoever set them.
func TestOneRingCoversTimersFromBothSides(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })

	now := time.Now()
	ts.Start("pasta", time.Minute)
	ts.ripe(now.Add(2 * time.Minute))
	first, _ := ts.RingingName()

	ts.Event(esphome.TimerEvent{
		Type:    api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED,
		TimerID: "kettle",
		Name:    "kettle",
	})
	if name, _ := ts.RingingName(); name != first {
		t.Errorf("the ringing changed to %q when a second timer finished, want the first to hold", name)
	}
}
