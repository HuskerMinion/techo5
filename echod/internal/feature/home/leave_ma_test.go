package home

import (
	"errors"
	"slices"
	"testing"
)

// A leave stops the room when the group cannot be known. The first version of this did the opposite — an
// unanswerable group meant an unjoin that failed and a return with no stop — so with no token, Home
// Assistant down, or a renamed Music Assistant player the track this room asked to replace carried on,
// which is the bug the whole leave exists to end.
func TestALeaveStopsWhenTheGroupCannotBeKnown(t *testing.T) {
	var unjoined, stopped bool
	err := leaveWith(nil, false, true,
		func() error { unjoined = true; return nil },
		func() { stopped = true })
	if err != nil {
		t.Fatalf("leaveWith = %v", err)
	}
	if unjoined {
		t.Error("the room was asked to leave a group nobody could say it was in")
	}
	if !stopped {
		t.Error("the room was left playing what it asked to replace")
	}
}

// A room that is certainly in a group leaves it first and stops after, in that order: the stop is what
// takes a group down, so a station started in one room would otherwise silence every other one.
func TestALeaveUnjoinsBeforeItStops(t *testing.T) {
	var order []string
	err := leaveWith([]any{"this room", "its partner"}, true, true,
		func() error { order = append(order, "unjoin"); return nil },
		func() { order = append(order, "stop") })
	if err != nil {
		t.Fatalf("leaveWith = %v", err)
	}
	if want := []string{"unjoin", "stop"}; !slices.Equal(order, want) {
		t.Errorf("the leave did %v, want %v", order, want)
	}
}

// A leave that could not be made is not followed by a stop: the room is still in the group, and the stop
// would take the group down with it. Playing along for another track is the safer of the two.
func TestALeaveThatCouldNotUnjoinDoesNotStop(t *testing.T) {
	var stopped bool
	err := leaveWith([]any{"this room"}, true, true,
		func() error { return errors.New("Home Assistant said no") },
		func() { stopped = true })
	if err == nil {
		t.Fatal("leaveWith lost the unjoin's failure")
	}
	if stopped {
		t.Error("the room was stopped while the group it is still in was playing")
	}
}

// A room on its own has no group to leave, so nothing is asked of Home Assistant for one; and nothing is
// stopped when there is nothing of this device's to stop.
func TestALeaveStopsOnlyWhatItWasCarrying(t *testing.T) {
	var unjoined int
	err := leaveWith(nil, true, true,
		func() error { unjoined++; return nil },
		func() {})
	if err != nil {
		t.Fatalf("leaveWith = %v", err)
	}
	if unjoined != 0 {
		t.Errorf("the room on its own was asked to unjoin %d times, want none", unjoined)
	}

	var stopped bool
	err = leaveWith([]any{"this room"}, true, false,
		func() error { return nil },
		func() { stopped = true })
	if err != nil {
		t.Fatalf("leaveWith = %v", err)
	}
	if stopped {
		t.Error("a stop was sent with nothing of this device's playing")
	}
}
