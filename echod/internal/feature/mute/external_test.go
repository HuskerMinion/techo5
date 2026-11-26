package mute

import "testing"

// A latch that moved with nobody at the button (the 2nd gen Show 8's enable line driven) is published
// at once; Home Assistant used to go on saying live over a cut microphone.
func TestExternalPublishesALatchMovedWithoutAPress(t *testing.T) {
	line := &fakeLine{acts: true}
	w := watch(line, false)
	line.muted = true
	w.external()
	if w.count() != 1 || !w.sw.Get() {
		t.Fatalf("published %v, switch %v: a latch set without a press was not followed", w.published, w.sw.Get())
	}
}

// A latch the button moved is left to the tap that follows: publishing it on the way down would take
// the state from before the press from the tap, and with it the chime.
func TestExternalLeavesAPressToItsTap(t *testing.T) {
	line := &fakeLine{acts: true}
	w := watch(line, false)
	w.press(true)
	line.muted = true
	w.external()
	if w.count() != 0 || w.sw.Get() {
		t.Fatalf("published %v while the button was down", w.published)
	}
}
