//go:build !dot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Swiping between clock styles is on by default, off with its switch, and off whenever Tap on the
// clock is Nothing.
func TestTheStyleSwipeSetting(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if !styleSwipeOn() {
		t.Error("off on a new device")
	}
	sw := styleSwipeSwitch()
	sw.OnCommand(false)
	if styleSwipeOn() || sw.Get() {
		t.Error("still on after the switch went off")
	}
	sw.OnCommand(true)
	if err := config.Set().Screen().ClockTap("nothing"); err != nil {
		t.Fatal(err)
	}
	if styleSwipeOn() {
		t.Error("on with Tap on the clock set to Nothing")
	}
}
