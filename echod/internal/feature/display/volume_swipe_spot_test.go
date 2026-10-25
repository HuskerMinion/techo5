//go:build spot

package display

import (
	"image"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// On the Spot's face a swipe up or down is the volume while Swipe for volume is on, and leaves it
// alone once it is off (#243); a swipe across the face still turns the clock style.
func TestASwipeOnTheSpotFollowsTheVolumeSetting(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	steps := []int{}
	was := adjustVolume
	adjustVolume = func(step int) { steps = append(steps, step) }
	t.Cleanup(func() { adjustVolume = was })
	d := &Display{r: newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side))), on: true, clockUp: true,
		poke: make(chan struct{}, 1), view: voice.State{Phase: "idle"}}
	d.volumeSwipeSw = volumeSwipeSwitch()

	d.gesture(touch.Gesture{Kind: touch.SwipeUp, X: center, Y: center})
	d.gesture(touch.Gesture{Kind: touch.SwipeDown, X: center, Y: center})
	if len(steps) != 2 || steps[0] != +1 || steps[1] != -1 {
		t.Fatalf("steps %v with the setting on, want [1 -1]", steps)
	}

	d.volumeSwipeSw.OnCommand(false)
	d.gesture(touch.Gesture{Kind: touch.SwipeUp, X: center, Y: center})
	d.gesture(touch.Gesture{Kind: touch.SwipeDown, X: center, Y: center})
	if len(steps) != 2 {
		t.Errorf("the volume moved by %v with the setting off", steps[2:])
	}
	before := clockStyleIndex()
	d.gesture(touch.Gesture{Kind: touch.SwipeLeft, X: center, Y: center})
	if clockStyleIndex() == before {
		t.Error("a swipe across the face did not turn the clock style")
	}
}
