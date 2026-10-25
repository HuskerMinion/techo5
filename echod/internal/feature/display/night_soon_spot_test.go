//go:build spot

package display

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

var idleView = voice.State{Phase: "idle"}

// Closing the settings asks for the night clock soon: it comes up nightSoon after, not a minute and a
// half after the tap that closed them (techo5-spot#7).
func TestTheSpotNightClockComesSoonAfterTheSettingsClose(t *testing.T) {
	d := spotNightDisplay(t, true)
	now := time.Now()
	d.night(now, true, idleView) // the night already under way
	d.mu.Lock()
	d.nightGlow = false
	d.sheetOpen = true
	d.touchedAt = now
	d.mu.Unlock()
	if d.night(now, true, idleView) || d.glowingNow() {
		t.Fatal("the night clock came up over the open settings")
	}
	d.mu.Lock()
	d.sheetOpen = false
	d.mu.Unlock()
	closed := now.Add(time.Second)
	if d.night(closed, true, idleView) || d.glowingNow() {
		t.Fatal("the night clock came up the moment the settings closed")
	}
	if !d.night(closed.Add(nightSoon+time.Second), true, idleView) || !d.glowingNow() {
		t.Fatal("the night clock did not come up soon after the settings closed")
	}
}

// A touch after the clock was asked for is somebody using the face again: the usual minute and a half.
func TestATouchAfterTheSettingsCloseWaitsTheUsualTime(t *testing.T) {
	d := spotNightDisplay(t, true)
	now := time.Now()
	d.night(now, true, idleView)
	d.mu.Lock()
	d.nightGlow = false
	d.sheetOpen = true
	d.touchedAt = now
	d.mu.Unlock()
	d.night(now, true, idleView)
	d.mu.Lock()
	d.sheetOpen = false
	d.mu.Unlock()
	closed := now.Add(time.Second)
	if d.night(closed, true, idleView) || d.glowingNow() {
		t.Fatal("the night clock came up the moment the settings closed")
	}
	d.mu.Lock()
	d.touchedAt = closed.Add(time.Second)
	d.mu.Unlock()
	if d.night(closed.Add(nightSoon+2*time.Second), true, idleView) || d.glowingNow() {
		t.Fatal("a touch after the settings closed did not hold the night clock back")
	}
	if !d.night(closed.Add(nightIdle+2*time.Second), true, idleView) || !d.glowingNow() {
		t.Fatal("the night clock did not come back a minute and a half after the touch")
	}
}

// Choosing Night clock, from Home Assistant say, brings it up soon even after a recent touch.
func TestChoosingTheNightClockBringsItSoon(t *testing.T) {
	d := spotNightDisplay(t, true)
	if err := config.Set().Screen().AtNightDark(false, false, false); err != nil { // Dim
		t.Fatal(err)
	}
	now := time.Now()
	d.night(now, true, idleView)
	d.mu.Lock()
	d.touchedAt = now.Add(-30 * time.Second)
	d.mu.Unlock()
	d.setAtNight(1) // Night clock
	if d.night(time.Now(), true, idleView) || d.glowingNow() {
		t.Fatal("the night clock came up with no wait at all after a touch half a minute ago")
	}
	if !d.night(time.Now().Add(nightSoon+time.Second), true, idleView) || !d.glowingNow() {
		t.Fatal("the night clock did not come up soon after it was chosen")
	}
}

// The scale starts at backlight 3, above the panel's lowest light, and the settings row sets it,
// keeps it in range, and shows it on the backlight at night.
func TestTheSpotNightClockBrightnessRow(t *testing.T) {
	d := spotNightDisplay(t, true)
	// The step's relight at the end of its preview is this test's, not the next one's: left to run it
	// reads the settings while the next test points them at its own file.
	t.Cleanup(func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.glowTimer != nil {
			d.glowTimer.Stop()
		}
	})
	if glowSteps[0] != 3 {
		t.Errorf("the night clock's lowest step is backlight %d, want 3", glowSteps[0])
	}
	if got := glowSteps[glowDefault-1]; got != 8 {
		t.Errorf("the night clock's default is backlight %d, want 8", got)
	}
	d.stepGlow(-20)
	if got := config.Get().Screen.NightLightLevel; got != 1 {
		t.Errorf("stepped all the way down, the level is %d, want 1", got)
	}
	d.mu.Lock()
	level := d.level
	d.mu.Unlock()
	if level != 3 {
		t.Errorf("at night a step shows backlight %v, want 3", level)
	}
	d.stepGlow(+20)
	if got := config.Get().Screen.NightLightLevel; got != len(glowSteps) {
		t.Errorf("stepped all the way up, the level is %d, want %d", got, len(glowSteps))
	}

	rows, _ := categoryRows(sheetView{st: settings{cat: catDisplay, night: nightHoursSet()}})
	found := false
	for _, r := range rows {
		if r.id == "nightglow" {
			found = true
		}
	}
	if !found {
		t.Error("no Night clock brightness row under Display with Night clock chosen")
	}
}
