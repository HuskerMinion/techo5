//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Dim after idle takes the screen down to its Dimmest once it has gone the wait untouched, not before,
// not at night or while something is going on, and a touch brings it straight back.
func TestIdleDimsAndATouchUndims(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	if err := config.Set().Screen().DimAfterMinutes(5); err != nil {
		t.Fatal(err)
	}
	d := &Display{on: true, ceiling: 100, poke: make(chan struct{}, 1)}
	start := time.Now()
	d.relight(true)
	full := d.level

	d.idleDimTick(start, true, false, false) // the first look starts the wait
	d.idleDimTick(start.Add(4*time.Minute), true, false, false)
	if d.idleDimmed {
		t.Fatal("dimmed before the wait")
	}
	d.idleDimTick(start.Add(4*time.Minute), true, true, false) // a turn: the wait starts again
	d.idleDimTick(start.Add(8*time.Minute), true, false, false)
	if d.idleDimmed {
		t.Fatal("dimmed four minutes after a turn")
	}
	d.idleDimTick(start.Add(9*time.Minute+time.Second), true, false, true)
	if d.idleDimmed {
		t.Fatal("dimmed at night, which has its own rules")
	}
	d.idleDimTick(start.Add(9*time.Minute+time.Second), true, false, false)
	if !d.idleDimmed {
		t.Fatal("not dimmed after the wait")
	}
	for range 100 {
		d.settleStep()
	}
	if want := idleDimLevel(full, 100); d.level > want+0.5 || d.level >= full {
		t.Fatalf("dimmed to %.1f, want %.1f (full %.1f)", d.level, want, full)
	}

	d.mu.Lock()
	d.touchedAt = start.Add(10 * time.Minute)
	d.mu.Unlock()
	d.idleDimTick(start.Add(10*time.Minute), true, false, false)
	if d.idleDimmed || d.level != full {
		t.Fatalf("a touch left it dimmed (%v) at %.1f, want %.1f at once", d.idleDimmed, d.level, full)
	}

	if err := config.Set().Screen().DimAfterMinutes(0); err != nil {
		t.Fatal(err)
	}
	d.idleDimTick(start.Add(time.Hour), true, false, false)
	if d.idleDimmed {
		t.Fatal("dimmed with Dim after idle at Never")
	}
}

// The Dim screen button takes the screen down to its Dimmest with Dim after idle at Never, and the
// next touch brings it straight back; it holds no longer than that, and the sensor follows it.
func TestDimScreenButtonDimsUntilTouched(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	d := &Display{on: true, ceiling: 100, poke: make(chan struct{}, 1), dimSensor: dimmedSensor()}
	d.relight(true)
	full := d.level
	start := time.Now()
	d.idleDimTick(start, true, false, false)

	d.dimScreen()
	d.mu.Lock()
	asked := d.dimAskedAt
	d.mu.Unlock()
	if asked.IsZero() {
		t.Fatal("the button was not taken")
	}
	d.idleDimTick(asked.Add(time.Second), true, false, false)
	if !d.idleDimmed || !d.dimSensor.Get() {
		t.Fatalf("not dimmed by the button (dimmed %v, sensor %v)", d.idleDimmed, d.dimSensor.Get())
	}
	for range 100 {
		d.settleStep()
	}
	if want := idleDimLevel(full, 100); d.level > want+0.5 || d.level >= full {
		t.Fatalf("dimmed to %.1f, want %.1f (full %.1f)", d.level, want, full)
	}

	d.mu.Lock()
	d.touchedAt = asked.Add(2 * time.Second)
	d.mu.Unlock()
	d.idleDimTick(asked.Add(2*time.Second), true, false, false)
	if d.idleDimmed || d.dimSensor.Get() || d.level != full {
		t.Fatalf("a touch left it dimmed (%v, sensor %v) at %.1f, want %.1f", d.idleDimmed, d.dimSensor.Get(), d.level, full)
	}
	d.idleDimTick(asked.Add(time.Hour), true, false, false)
	if d.idleDimmed {
		t.Fatal("dimmed again an hour later with Dim after idle at Never: the button outlived the touch")
	}
}

// Anything that counts as use undoes the button as a touch does: something going on (a turn), and the
// screen turned on or its brightness set (apply, which marks usedAt). On a screen that is out it does
// nothing.
func TestDimScreenButtonGivesWayToUse(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	d := &Display{on: true, ceiling: 100, poke: make(chan struct{}, 1), dimSensor: dimmedSensor()}
	start := time.Now()
	d.idleDimTick(start, true, false, false)

	d.dimScreen()
	d.idleDimTick(time.Now(), true, true, false) // a turn going on when it was pressed
	if d.idleDimmed {
		t.Fatal("dimmed during a turn")
	}
	d.idleDimTick(time.Now().Add(time.Minute), true, false, false)
	if d.idleDimmed {
		t.Fatal("dimmed after the turn: the turn should have used the press up")
	}

	d.dimScreen()
	d.idleDimTick(time.Now().Add(time.Second), true, false, false)
	if !d.idleDimmed {
		t.Fatal("not dimmed by the button")
	}
	d.mu.Lock()
	d.usedAt = time.Now().Add(2 * time.Second) // Home Assistant set the light, as apply marks it
	d.mu.Unlock()
	d.idleDimTick(time.Now().Add(2*time.Second), true, false, false)
	if d.idleDimmed || d.dimSensor.Get() {
		t.Fatal("setting the light left it dimmed")
	}

	d.mu.Lock()
	d.on = false
	d.mu.Unlock()
	d.dimScreen()
	d.mu.Lock()
	asked := d.dimAskedAt
	d.mu.Unlock()
	if !asked.IsZero() {
		t.Fatal("the button was taken on a screen that is out")
	}
}

// Dim after idle sets the sensor too.
func TestScreenDimmedSensorFollowsDimAfterIdle(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	if err := config.Set().Screen().DimAfterMinutes(1); err != nil {
		t.Fatal(err)
	}
	d := &Display{on: true, ceiling: 100, poke: make(chan struct{}, 1), dimSensor: dimmedSensor()}
	start := time.Now()
	d.idleDimTick(start, true, false, false)
	d.idleDimTick(start.Add(time.Minute+time.Second), true, false, false)
	if !d.dimSensor.Get() {
		t.Fatal("the sensor missed Dim after idle")
	}
	d.idleDimTick(start.Add(2*time.Minute), true, true, false)
	if d.dimSensor.Get() {
		t.Fatal("the sensor stayed on with the screen back up")
	}
}

// A press that lands after the frame took its now, with the screen in use (a turn going on), is
// dropped: the screen never starts to fade and Screen dimmed never turns on, then or later.
func TestDimScreenPressDuringUseIsDropped(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	d := &Display{on: true, ceiling: 100, poke: make(chan struct{}, 1), dimSensor: dimmedSensor()}
	d.idleDimTick(time.Now().Add(-time.Minute), true, false, false)

	d.dimScreen()
	d.mu.Lock()
	asked := d.dimAskedAt
	d.mu.Unlock()
	frame := asked.Add(-10 * time.Millisecond) // the frame's now, taken before the press
	d.idleDimTick(frame, true, true, false)
	if d.idleDimmed || d.dimSensor.Get() {
		t.Fatal("a press during a turn dimmed the screen for a frame")
	}
	d.idleDimTick(asked.Add(time.Second), true, false, false)
	if d.idleDimmed || d.dimSensor.Get() {
		t.Fatal("a press dropped during a turn dimmed the screen once the turn was over")
	}
}

// A press the night keeps from dimming is forgotten, not carried out when the night ends.
func TestDimScreenPressIsNotSavedUpOverNight(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	d := &Display{on: true, ceiling: 100, poke: make(chan struct{}, 1), dimSensor: dimmedSensor()}
	d.idleDimTick(time.Now().Add(-time.Minute), true, false, false)

	d.dimScreen() // by day, a moment before the night starts
	d.mu.Lock()
	asked := d.dimAskedAt
	d.mu.Unlock()
	d.idleDimTick(asked.Add(time.Second), true, false, true) // the first look is already at night
	if d.idleDimmed {
		t.Fatal("dimmed at night")
	}
	d.idleDimTick(asked.Add(8*time.Hour), true, false, false) // the night is over, nobody used it
	if d.idleDimmed || d.dimSensor.Get() {
		t.Fatal("a press from before the night dimmed the screen when it ended")
	}
}
