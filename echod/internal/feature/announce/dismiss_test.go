package announce

import (
	"testing"
	"time"
)

// Putting one away takes it off the screen now.
//
// On a round face an announcement has the whole screen, and until this there was no way past it: the
// screen sat there for its full time with nothing else reachable, for something that had finished
// being said seconds earlier.
func TestPuttingAnAnnouncementAway(t *testing.T) {
	f := &Feature{}
	f.mu.Lock()
	f.last, f.until = Message{From: "Kitchen"}, time.Now().Add(shows)
	f.mu.Unlock()

	if _, showing := f.Showing(); !showing {
		t.Fatal("not showing to begin with")
	}

	f.Dismiss()

	if _, showing := f.Showing(); showing {
		t.Error("still showing after being put away")
	}
}

// Putting away when there is nothing there does nothing, since a finger can land at any moment,
// including just after one timed out by itself.
func TestPuttingAwayNothing(t *testing.T) {
	f := &Feature{}
	f.Dismiss()
	if _, showing := f.Showing(); showing {
		t.Error("showing something that was never there")
	}
}

// A round face gives an announcement less time than a strip does, because it gives it the whole
// screen. This is the constant that says so, per device, and it is easy to lose in a merge.
func TestHowLongOneStays(t *testing.T) {
	if shows <= 0 || shows > time.Minute {
		t.Errorf("shows is %v, which is not a length anybody meant", shows)
	}
}
