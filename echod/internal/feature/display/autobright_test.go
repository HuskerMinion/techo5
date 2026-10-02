//go:build !dot

package display

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A change in the room's light arrives as one reading, or a few while it settles, and then the sensor
// goes quiet: the input layer drops a value equal to the last, so a steady room sends nothing. Each
// reading moves the backlight only part of the way, so it has to keep moving between readings, or it
// is left short of the room's level until something else relights the screen - which is what the
// auto-brightness switch and the brightness arrows were doing.
func TestAutoBrightnessFinishesWithoutMoreReadings(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))

	d := &Display{on: true, autoOn: true, ceiling: 100}
	d.relight(true)
	d.mu.Lock()
	target := d.level
	d.level = 1 // where the backlight stood before the room changed
	d.mu.Unlock()

	d.relight(false) // the one reading the change sent
	d.mu.Lock()
	partway := d.level
	d.mu.Unlock()
	if math.Abs(target-partway) < 1 {
		t.Fatalf("one reading took the backlight to %.1f, all the way to %.1f: nothing left to test", partway, target)
	}

	for range 100 {
		d.settleStep()
	}
	d.mu.Lock()
	got := d.level
	d.mu.Unlock()
	if math.Abs(target-got) > 0.5 {
		t.Fatalf("with no more readings the backlight stopped at %.1f of %.1f (it was %.1f after the reading)", got, target, partway)
	}
}
