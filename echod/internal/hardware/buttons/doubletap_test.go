package buttons

import (
	"testing"
	"time"
)

// The second tap of a pair is reported after both taps, not instead of them, because the first one
// has already acted by the time the second arrives. What must not happen is a double appearing from
// two presses that were never meant as one, or a run of taps reading as double after double.
func TestDoubleTapFollowsThePair(t *testing.T) {
	c := &Controller{}
	var got []Event
	defer c.Events.Listen(func(e Event) { got = append(got, e) })()

	c.emit(Action, Tap)
	c.emit(Action, Tap)

	want := []Event{{Action, Tap}, {Action, Tap}, {Action, DoubleTap}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// Three taps are a double and then a single. Without spending the pair, the third tap would pair
// with the second and every tap after the first would be a double.
func TestThreeTapsAreNotTwoDoubles(t *testing.T) {
	c := &Controller{}
	doubles := 0
	defer c.Events.Listen(func(e Event) {
		if e.Kind == DoubleTap {
			doubles++
		}
	})()

	c.emit(Action, Tap)
	c.emit(Action, Tap)
	c.emit(Action, Tap)

	if doubles != 1 {
		t.Errorf("three taps gave %d doubles, want 1", doubles)
	}
}

// Two presses far enough apart are two presses.
func TestTapsApartAreNotADouble(t *testing.T) {
	c := &Controller{}
	doubles := 0
	defer c.Events.Listen(func(e Event) {
		if e.Kind == DoubleTap {
			doubles++
		}
	})()

	c.emit(Action, Tap)
	c.mu.Lock()
	c.tapped[Action] = time.Now().Add(-doubleTap - time.Millisecond)
	c.mu.Unlock()
	c.emit(Action, Tap)

	if doubles != 0 {
		t.Errorf("taps %v apart gave a double", doubleTap)
	}
}

// Volume steps once per tap and ramps while it is held; a pair of steps is not a gesture.
func TestVolumeHasNoDoubleTap(t *testing.T) {
	c := &Controller{}
	doubles := 0
	defer c.Events.Listen(func(e Event) {
		if e.Kind == DoubleTap {
			doubles++
		}
	})()

	c.emit(VolumeUp, Tap)
	c.emit(VolumeUp, Tap)

	if doubles != 0 {
		t.Error("volume reported a double tap")
	}
}

// Two different buttons tapped in quick succession are not a double of either.
func TestADoubleIsOneButton(t *testing.T) {
	c := &Controller{}
	doubles := 0
	defer c.Events.Listen(func(e Event) {
		if e.Kind == DoubleTap {
			doubles++
		}
	})()

	c.emit(Action, Tap)
	c.emit(Mute, Tap)

	if doubles != 0 {
		t.Error("two different buttons made a double tap")
	}
}
