package ring

import (
	"sync"
	"testing"
	"time"
)

func TestNothingSoundsWhenNothingRings(t *testing.T) {
	quietBell(t, time.Minute)
	// Waited for, since an earlier test's last write may still be on its way.
	waitFor(t, "the sensors to read quiet", func() bool {
		return !sounding.Get() && !alarmSounding.Get() && !timerSounding.Get() && soundingWhat.Get() == ""
	})
}

func TestARingTurnsTheSensorsOnAndStoppingItTurnsThemOff(t *testing.T) {
	quietBell(t, time.Minute)
	var e ends

	stop := Start("alarm", "Wake up", nil, e.ended)
	// Written by the publisher, not by Start, so waited for.
	waitFor(t, "the sensors to say an alarm rings", func() bool {
		return sounding.Get() && alarmSounding.Get() && !timerSounding.Get() && soundingWhat.Get() == `alarm "Wake up"`
	})

	stop()
	waitFor(t, "the sensors to clear", func() bool { return !sounding.Get() })
	if alarmSounding.Get() || soundingWhat.Get() != "" {
		t.Errorf("alarm %v what %q after the only ring ended", alarmSounding.Get(), soundingWhat.Get())
	}
}

// The reason for a count and not a flag, again: the first to finish must not report silence.
func TestAnAlarmAndATimerTogetherStayOnUntilTheLastEnds(t *testing.T) {
	quietBell(t, time.Minute)
	var a, tm ends

	stopAlarm := Start("alarm", "Wake up", nil, a.ended)
	stopTimer := Start("timer", "Pasta", nil, tm.ended)
	waitFor(t, "both to be on", func() bool {
		return alarmSounding.Get() && timerSounding.Get() && soundingWhat.Get() == `alarm "Wake up", timer "Pasta"`
	})

	stopAlarm()
	waitFor(t, "the alarm to end", func() bool { return !alarmSounding.Get() })
	if !sounding.Get() || !timerSounding.Get() {
		t.Error("the timer is still ringing and a sensor says otherwise")
	}
	if got := soundingWhat.Get(); got != `timer "Pasta"` {
		t.Errorf("what says %q after the alarm ended", got)
	}

	stopTimer()
	waitFor(t, "everything to clear", func() bool { return !sounding.Get() })
	if timerSounding.Get() || soundingWhat.Get() != "" {
		t.Error("the timer's sensors outlived its ring")
	}
}

func TestAnUnlabeledRingIsNamedByWhatItIs(t *testing.T) {
	quietBell(t, time.Minute)
	var e ends
	stop := Start("timer", "", nil, e.ended)
	defer stop()
	waitFor(t, "the words to say timer", func() bool { return soundingWhat.Get() == "timer" })
}

// A ring nobody stops rings out, and the sensors must follow it off rather than stick on.
func TestARingThatRingsOutClearsTheSensors(t *testing.T) {
	quietBell(t, 20*time.Millisecond)
	var e ends
	Start("alarm", "Wake up", nil, e.ended)
	waitFor(t, "the ring to ring out", func() bool { return !sounding.Get() })
	if alarmSounding.Get() || soundingWhat.Get() != "" {
		t.Error("the sensors stuck on after the ring rang out")
	}
}

// Silenced by a button and waiting on the snooze offer, a ring is still active: on means a ring is
// active, not that it is audible.
func TestASilencedRingStillCountsAsActive(t *testing.T) {
	quietBell(t, time.Minute)
	var e ends
	stop := Start("alarm", "Wake up", nil, e.ended)
	defer stop()
	silenceBell()
	waitFor(t, "the sensors to read on", func() bool { return sounding.Get() && alarmSounding.Get() })
	// Given the publisher time to have been wrong, if it was going to be.
	time.Sleep(20 * time.Millisecond)
	if !sounding.Get() || !alarmSounding.Get() {
		t.Error("a silenced ring reads as over while its offer stands")
	}
}

// The alarm and the timer call Start with their own locks held, so a sensor write that waits on a
// half-dead Home Assistant connection must never be on the way to a ring starting or ending.
func TestASlowSensorWriteDoesNotHoldUpARing(t *testing.T) {
	quietBell(t, time.Minute)

	release := make(chan struct{})
	active.mu.Lock()
	was := setSensors
	setSensors = func(list []*activeRing) { <-release; was(list) }
	active.mu.Unlock()
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		free()
		active.mu.Lock()
		setSensors = was
		active.mu.Unlock()
	})

	var e ends
	started := make(chan func())
	go func() { started <- Start("alarm", "Wake up", nil, e.ended) }()
	var stop func()
	select {
	case stop = <-started:
	case <-time.After(time.Second):
		t.Fatal("Start waited on a sensor write")
	}
	if !IsSounding() {
		t.Error("not sounding while the sensors are stuck")
	}

	stop()
	waitFor(t, "the ring to end while the sensors are stuck", func() bool { return e.count() > 0 && !IsSounding() })

	free()
	waitFor(t, "the sensors to catch up", func() bool { return !sounding.Get() && soundingWhat.Get() == "" })
}
