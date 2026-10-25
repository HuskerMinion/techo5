//go:build !dot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Swipe for volume is on by default, so nothing changes on an update, and off with its switch, which
// a swipe's step then does not get past (#243).
func TestTheVolumeSwipeSetting(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	steps := 0
	was := adjustVolume
	adjustVolume = func(int) { steps++ }
	t.Cleanup(func() { adjustVolume = was })

	if !volumeSwipeOn() {
		t.Error("off on a new device")
	}
	sw := volumeSwipeSwitch()
	swipeVolume(+1)
	if steps != 1 {
		t.Errorf("%d steps with the setting on, want 1", steps)
	}
	sw.OnCommand(false)
	if volumeSwipeOn() || sw.Get() || !config.Get().Screen.NoVolumeSwipe {
		t.Error("still on after the switch went off")
	}
	swipeVolume(-1)
	if steps != 1 {
		t.Error("a swipe moved the volume with the setting off")
	}
	sw.OnCommand(true)
	if !volumeSwipeOn() || !sw.Get() {
		t.Error("still off after the switch went on")
	}
}
