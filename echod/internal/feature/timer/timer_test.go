package timer

import (
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
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

func TestACanceledTimerIsForgotten(t *testing.T) {
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

// What Home Assistant is told is what the screen shows: each timer by name, soonest first, and what is
// left of it. A name on its own would not say whether it is the timer somebody is waiting on — and after a
// restart of Home Assistant, which takes its timers with it, this sensor is the only place one can still
// be watched.
// What Home Assistant is told is each timer by name, soonest first, and when it finishes — not what is
// left of it. The sensor is published when a timer starts or ends, so a countdown in it would stand still
// there; a time of day stays right until the timer goes off.
func TestWhatIsCountingDownIsNamedSoonestFirstByWhenItFinishes(t *testing.T) {
	now := time.Date(2026, 9, 30, 18, 38, 0, 0, time.Local)
	ts := build()
	ts.Event(started("pasta", 600))
	ts.Event(started("eggs", 240))

	// Started at a fixed moment, so when they finish is exact rather than a second out.
	set := func() {
		ts.mu.Lock()
		for _, c := range ts.held {
			c.at = now
		}
		ts.mu.Unlock()
	}
	set()

	// The screen's own setting decides the form a time is written in, twelve hour or twenty-four, so a
	// timer read in Home Assistant is written the way the panel would write it.
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().Clock24(true); err != nil {
		t.Fatal(err)
	}
	if got := ts.describe(now); got != "eggs at 18:42, pasta at 18:48" {
		t.Errorf("describing %q, want %q", got, "eggs at 18:42, pasta at 18:48")
	}

	if err := config.Set().Screen().Clock24(false); err != nil {
		t.Fatal(err)
	}
	if got := ts.describe(now); got != "eggs at 6:42 PM, pasta at 6:48 PM" {
		t.Errorf("describing %q, want %q", got, "eggs at 6:42 PM, pasta at 6:48 PM")
	}

	// And the soonest first, whoever set them.
	ts.Event(esphome.TimerEvent{
		Type:    api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_CANCELLED,
		TimerID: "eggs",
	})
	if got := ts.describe(now); got != "pasta at 6:48 PM" {
		t.Errorf("describing %q, want %q", got, "pasta at 6:48 PM")
	}
}
func TestAnUnnamedTimerStillHasSomethingToCallIt(t *testing.T) {
	now := time.Date(2026, 9, 30, 18, 38, 0, 0, time.Local)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().Clock24(true); err != nil {
		t.Fatal(err)
	}
	ts := build()
	e := started("kettle", 180)
	e.Name = ""
	ts.Event(e)
	ts.mu.Lock()
	for _, c := range ts.held {
		c.at = now
	}
	ts.mu.Unlock()

	if got := ts.describe(now); got != "Timer at 18:41" {
		t.Errorf("describing %q, want %q", got, "Timer at 18:41")
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

// Cancel is for the device's own timers. Home Assistant's are canceled where they were set, and it
// says so itself, so a tap here must not make the two disagree.
func TestOnlyTheDevicesOwnTimersAreCanceledHere(t *testing.T) {
	ts := build()
	ts.Event(started("kettle", 180))
	id := ts.Start("pasta", time.Minute)

	if ts.Cancel("kettle") {
		t.Error("canceled a Home Assistant timer from the device")
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

// ranOut moves a timer so that it ran out the given time ago, without waiting for it.
func ranOut(ts *Timers, id string, ago time.Duration, now time.Time) {
	ts.mu.Lock()
	if c := ts.held[id]; c != nil {
		c.at = now.Add(-ago - c.left)
	}
	ts.mu.Unlock()
}

// A timer from Home Assistant whose finishing event never arrives — the device was updating, restarting or
// offline when it ran out — must not sit at zero forever. Nothing else ends it: only the device's own
// timers run out from this clock, and the event is what ends Home Assistant's.
func TestATimerWhoseEventNeverArrivesIsEndedHere(t *testing.T) {
	now := time.Now()
	ts := build()
	t.Cleanup(func() { ts.Stop() })

	ts.Event(started("kettle", 180))

	// The moment it runs out: the event for it is on its way over the link it was set over, so nothing is
	// decided yet.
	ranOut(ts, "kettle", 0, now)
	ts.ripe(now)
	if len(ts.held) != 1 {
		t.Fatal("a timer was ended the instant it ran out, before its event could arrive")
	}

	// Long past any event that was coming: it is ended here, and rung, since it has only just gone.
	ranOut(ts, "kettle", lostAfter+time.Second, now)
	ts.ripe(now)
	if len(ts.held) != 0 {
		t.Errorf("holding %d timers, want none: a timer whose event never came is ended here", len(ts.held))
	}
	if !ts.Ringing() {
		t.Error("a timer that had only just run out was not rung when the device ended it")
	}
	ts.Stop()

	// The ring lets go a moment later, as it does on the device.
	for range 100 {
		if !ts.Ringing() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ts.Ringing() {
		t.Fatal("a timer that had run out stayed ringing after being stopped")
	}

	// An event arriving afterwards does not ring it a second time.
	ts.Event(esphome.TimerEvent{
		Type:    api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED,
		TimerID: "kettle",
		Name:    "kettle",
	})
	if ts.Ringing() {
		t.Error("a late finishing event rang a timer the device had already ended")
	}
}

// One that ran out hours ago is recorded as missed rather than rung: a timer nobody heard is not made right
// by sounding now. That is what a timer of the device's own gets in the same case.
func TestATimerThatRanOutLongAgoIsRecordedAsMissed(t *testing.T) {
	now := time.Now()
	ts := build()
	t.Cleanup(func() { ts.Stop() })

	ts.Event(started("kettle", 180))
	ranOut(ts, "kettle", 3*time.Hour, now)

	ts.ripe(now)
	if len(ts.held) != 0 {
		t.Errorf("holding %d timers, want none", len(ts.held))
	}
	if ts.Ringing() {
		t.Error("a timer that ran out three hours ago was rung")
	}
}

// And a timer of the device's own is untouched by any of this: it ends when it ends, whether or not
// anything else is listening.
func TestALocalTimerStillEndsItself(t *testing.T) {
	now := time.Now()
	ts := build()
	t.Cleanup(func() { ts.Stop() })

	ts.Start("kettle", 60*time.Second)
	ts.mu.Lock()
	for _, c := range ts.held {
		c.at = now.Add(-61 * time.Second)
	}
	ts.mu.Unlock()

	ts.ripe(now)
	if len(ts.held) != 0 {
		t.Errorf("holding %d timers, want none", len(ts.held))
	}
	if !ts.Ringing() {
		t.Error("a timer of the device's own that ran out did not ring")
	}
}

// The voice subscription ending does not mean Home Assistant's timers are over. One that is still counting
// keeps counting there, so the device drops it quietly rather than ringing a timer nobody is waiting for.
func TestForgetDropsHomeAssistantsTimerThatIsStillCounting(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })
	ts.Event(started("kettle", 600))

	ts.Forget()

	if len(ts.held) != 0 {
		t.Errorf("holding %d timers, want none: Home Assistant stopped listening", len(ts.held))
	}
	if ts.Ringing() {
		t.Error("a timer that was still counting was rung when Home Assistant stopped listening")
	}
}

// And one that has already run out is ended the way the clock ends one of the device's own: rung, when it
// has only just gone. A timer set for ten minutes goes off at ten minutes even if the subscription ended
// underneath it.
func TestForgetEndsHomeAssistantsTimerThatHasRunOut(t *testing.T) {
	now := time.Now()
	ts := build()
	t.Cleanup(func() { ts.Stop() })
	ts.Event(started("kettle", 180))
	ranOut(ts, "kettle", lostAfter+time.Second, now)

	ts.Forget()

	if len(ts.held) != 0 {
		t.Errorf("holding %d timers, want none", len(ts.held))
	}
	if !ts.Ringing() {
		t.Error("a timer that had just run out was dropped without ringing when Home Assistant stopped listening")
	}
}

// A timer dropped because Home Assistant stopped listening is not one the device ended: Home Assistant
// still has it, and when it finishes there its event is the only ring it gets.
func TestATimerForgottenWhileCountingStillRingsWhenItFinishes(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })
	ts.Event(started("kettle", 600))

	ts.Forget()
	ts.Event(esphome.TimerEvent{
		Type:    api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED,
		TimerID: "kettle",
		Name:    "kettle",
	})

	if !ts.Ringing() {
		t.Error("a timer dropped while it was still counting did not ring when Home Assistant finished it")
	}
}

// A paused timer has no moment it runs out, so forgetting one drops it quietly: it is neither rung nor
// recorded as missed, however long ago it was paused.
func TestForgetDropsAPausedTimerQuietly(t *testing.T) {
	now := time.Now()
	ts := build()
	t.Cleanup(func() { ts.Stop(); ring.ClearMissed() })
	ring.ClearMissed()

	paused := started("kettle", 180)
	paused.IsActive = false
	ts.Event(paused)
	ranOut(ts, "kettle", 3*time.Hour, now)

	ts.Forget()

	if len(ts.held) != 0 {
		t.Errorf("holding %d timers, want none", len(ts.held))
	}
	if ts.Ringing() {
		t.Error("a paused timer was rung when Home Assistant stopped listening")
	}
	if got := ring.Missing(); len(got) != 0 {
		t.Errorf("a paused timer was recorded as missed: %v", got)
	}
	if len(ts.ended) != 0 {
		t.Error("a paused timer was taken for one the device had ended")
	}
}

// What the device ended is only remembered for as long as a late event could still be on its way, and a
// finishing event is where it is looked at, so that is where it is let go.
func TestWhatWasEndedHereIsLetGoOfWhenAnEventComes(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })
	ts.ended = map[string]time.Time{"kettle": time.Now().Add(-resumeWithin - time.Second)}

	ts.Event(esphome.TimerEvent{
		Type:    api.VoiceAssistantTimerEvent_VOICE_ASSISTANT_TIMER_FINISHED,
		TimerID: "pasta",
		Name:    "pasta",
	})

	if len(ts.ended) != 0 {
		t.Errorf("still remembering %d timers ended here long ago", len(ts.ended))
	}
}
