//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// volumeSwipeDisplay is a lit, idle clock with the volume's steps counted rather than taken.
func volumeSwipeDisplay(t *testing.T) (*Display, *[]int) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().Welcomed(true); err != nil {
		t.Fatal(err)
	}
	steps := []int{}
	was := adjustVolume
	adjustVolume = func(step int) { steps = append(steps, step) }
	t.Cleanup(func() { adjustVolume = was })
	d := &Display{r: testRenderer(), on: true, clockUp: true, poke: make(chan struct{}, 1), view: voice.State{Phase: "idle"}}
	d.volumeSwipeSw = volumeSwipeSwitch()
	return d, &steps
}

// A swipe up or down on the clock is the volume while Swipe for volume is on, as it always was (#243).
func TestASwipeOnTheClockIsTheVolume(t *testing.T) {
	d, steps := volumeSwipeDisplay(t)
	d.gesture(touch.Gesture{Kind: touch.SwipeUp, X: 480, Y: 240})
	d.gesture(touch.Gesture{Kind: touch.SwipeDown, X: 480, Y: 240})
	if len(*steps) != 2 || (*steps)[0] != +1 || (*steps)[1] != -1 {
		t.Errorf("steps %v, want [1 -1]", *steps)
	}
}

// With Swipe for volume off, a swipe up or down on the clock leaves the volume alone (#243), and the
// rest is as before: down from the top edge is the settings, up from the bottom edge the deck, and a
// swipe across the clock its next style.
func TestASwipeLeavesTheVolumeWithTheSettingOff(t *testing.T) {
	d, steps := volumeSwipeDisplay(t)
	d.volumeSwipeSw.OnCommand(false)
	d.gesture(touch.Gesture{Kind: touch.SwipeUp, X: 480, Y: 240})
	d.gesture(touch.Gesture{Kind: touch.SwipeDown, X: 480, Y: 240})
	if len(*steps) != 0 {
		t.Errorf("the volume moved by %v with the setting off", *steps)
	}

	d.gesture(touch.Gesture{Kind: touch.SwipeDown, X: 480, Y: 5})
	if !d.sheet {
		t.Error("a swipe down from the top edge did not open the settings")
	}
	d.showSheet(false)

	before := clockStyleIndex()
	d.gesture(touch.Gesture{Kind: touch.SwipeLeft, X: 480, Y: 240})
	if clockStyleIndex() == before {
		t.Error("a swipe across the clock did not turn its style")
	}

	if err := config.Set().Deck().Button(0, 0, config.DeckButton{Action: config.DeckOBSStream}); err != nil {
		t.Fatal(err)
	}
	d.gesture(touch.Gesture{Kind: touch.SwipeUp, X: 480, Y: d.r.h - 2})
	if !d.deckUp {
		t.Error("a swipe up from the bottom edge did not open the deck")
	}
	if len(*steps) != 0 {
		t.Errorf("the volume moved by %v with the setting off", *steps)
	}
}
