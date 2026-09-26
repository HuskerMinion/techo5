package announce

import (
	"context"
	"testing"
	"time"
)

// Finish and Cancel do nothing when nothing is being recorded. The screen calls them from a finger
// on the panel, which can land at any moment, including just after a recording ended by itself.
func TestFinishingWhenNothingIsBeingRecorded(t *testing.T) {
	f := &Feature{}
	f.Finish()
	f.Cancel()
	f.Finish()
}

// A second Finish must not close the channel twice. Two taps in quick succession is one finger
// bouncing, not a reason to take the daemon down.
func TestFinishingTwice(t *testing.T) {
	f := &Feature{finish: make(chan struct{})}
	f.Finish()
	f.Finish()
}

// Canceling reaches the recording: record returns on ctx, and this is the handle the screen has on
// it. Without this the only way off the recording screen was to wait out the ceiling.
func TestCancelingEndsTheRecording(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &Feature{cancel: cancel}

	f.Cancel()

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the recording was never told to stop")
	}
}
