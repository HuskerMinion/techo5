//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// updateDisplay is a Display with just enough to take a gesture and hear from the installer.
func updateDisplay(t *testing.T) *Display {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	return &Display{
		poke: make(chan struct{}, 1),
		on:   true,
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
	}
}

// A tap on the update dialog puts it away and the install carries on, rather than the tap being
// swallowed by a dialog that stays up.
func TestUpdateDialogTapDismisses(t *testing.T) {
	d := updateDisplay(t)
	d.updateActive = true
	d.updateSeen = time.Now()

	d.gesture(touch.Gesture{Kind: touch.Tap, X: 400, Y: 300})

	d.mu.Lock()
	dismissed, shown := d.updateDismissed, d.updateShown(time.Now())
	d.mu.Unlock()
	if !dismissed || shown {
		t.Fatal("a tap on the update dialog did not dismiss it")
	}
	// The tap was the dialog's: it did not reach the first-run card behind it.
	if config.Get().Screen.Welcomed {
		t.Error("the tap reached the page behind the dialog")
	}
}

// A swipe or a hold on the dialog does nothing at all, and does not dismiss it either.
func TestUpdateDialogIgnoresSwipeAndHold(t *testing.T) {
	for _, kind := range []touch.Kind{touch.SwipeUp, touch.SwipeDown, touch.Hold} {
		d := updateDisplay(t)
		d.updateActive = true
		d.updateSeen = time.Now()
		d.gesture(touch.Gesture{Kind: kind, X: 400, Y: 300})

		d.mu.Lock()
		dismissed, shown := d.updateDismissed, d.updateShown(time.Now())
		d.mu.Unlock()
		if dismissed || !shown {
			t.Errorf("%v on the update dialog changed its state", kind)
		}
	}
}

// A new install clears an earlier dismissal, so the dialog comes back for the next one.
func TestUpdateDialogShowsAgainForANewInstall(t *testing.T) {
	d := updateDisplay(t)
	d.updateActive = true
	d.gesture(touch.Gesture{Kind: touch.Tap, X: 400, Y: 300})

	d.updateChanged(false, 0, "", "", "") // the install ended
	d.updateChanged(true, 0, "downloading", "v2", "")

	d.mu.Lock()
	shown := d.updateShown(time.Now())
	d.mu.Unlock()
	if !shown {
		t.Error("a new install did not show the dialog again")
	}
}

// An install that starts while the screen is off does not light it: at 4am that lights a bedroom, and
// a screen Home Assistant turned off stays off.
func TestUpdateDoesNotLightAScreenThatIsOff(t *testing.T) {
	d := updateDisplay(t)
	d.mu.Lock()
	d.on = false
	d.mu.Unlock()

	d.updateChanged(true, 0.5, "downloading", "v2", "")

	d.mu.Lock()
	on := d.on
	d.mu.Unlock()
	if on {
		t.Error("starting an update turned the screen on")
	}
}

// A dialog that stops hearing from the installer is forgotten, so a stuck install does not leave the
// screen showing a dialog that never finishes.
func TestStaleUpdateDialogIsDropped(t *testing.T) {
	d := updateDisplay(t)
	d.updateActive = true
	d.updateProgress = 0.5
	d.updateSeen = time.Now().Add(-updateStale - time.Second)

	d.mu.Lock()
	d.expireUpdateLocked(time.Now())
	active := d.updateActive
	d.mu.Unlock()
	if active {
		t.Error("a stale update dialog was kept")
	}
}

// A failure's message is dropped once its window has passed, and no sooner.
func TestUpdateFailureExpires(t *testing.T) {
	d := updateDisplay(t)
	d.updateChanged(false, 0, "failed", "v2", "the download failed")

	d.mu.Lock()
	shown := d.updateShown(time.Now())
	d.mu.Unlock()
	if !shown {
		t.Fatal("the failure message was not shown at once")
	}

	d.mu.Lock()
	d.expireUpdateLocked(time.Now().Add(13 * time.Second))
	shown = d.updateShown(time.Now().Add(13 * time.Second))
	d.mu.Unlock()
	if shown {
		t.Error("the failure message outlived its window")
	}
}
