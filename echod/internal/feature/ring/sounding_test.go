package ring

import (
	"testing"
	"time"
)

func TestNothingSoundsWhenNothingRings(t *testing.T) {
	quietBell(t, time.Minute)
	if sounding.Get() || alarmSounding.Get() || timerSounding.Get() || soundingWhat.Get() != "" {
		t.Error("a sensor says something is ringing on a quiet device")
	}
}

func TestARingTurnsTheSensorsOnAndStoppingItTurnsThemOff(t *testing.T) {
	quietBell(t, time.Minute)
	var e ends

	stop := Start("alarm", "Wake up", nil, e.ended)
	// On before Start returns, like IsSounding: nothing sees a ring started and unreported.
	if !sounding.Get() || !alarmSounding.Get() || timerSounding.Get() {
		t.Errorf("sounding %v alarm %v timer %v, want true true false", sounding.Get(), alarmSounding.Get(), timerSounding.Get())
	}
	if got := soundingWhat.Get(); got != `alarm "Wake up"` {
		t.Errorf("what says %q", got)
	}

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
	if !alarmSounding.Get() || !timerSounding.Get() {
		t.Fatal("not both on while both ring")
	}
	if got := soundingWhat.Get(); got != `alarm "Wake up", timer "Pasta"` {
		t.Errorf("what says %q", got)
	}

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
	if got := soundingWhat.Get(); got != "timer" {
		t.Errorf("what says %q, want timer", got)
	}
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
	if !sounding.Get() || !alarmSounding.Get() {
		t.Error("a silenced ring reads as over while its offer stands")
	}
}
