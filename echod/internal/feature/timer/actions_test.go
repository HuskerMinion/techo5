package timer

import (
	"testing"
	"time"
)

func TestHomeAssistantCancelsOnlyTheDevicesOwnTimers(t *testing.T) {
	ts := build()
	ts.Event(started("kettle", 180)) // Home Assistant's, set by voice
	pasta := ts.Start("Pasta", 10*time.Minute)
	eggs := ts.Start("Eggs", 7*time.Minute)

	if err := ts.cancelFromHA("kettle"); err == nil {
		t.Error("cancelling Home Assistant's own timer from here should be refused")
	}
	if err := ts.cancelFromHA("local:nope"); err == nil {
		t.Error("cancelling a timer that does not exist should fail")
	}
	if err := ts.cancelFromHA(pasta); err != nil {
		t.Fatalf("cancelling %s: %v", pasta, err)
	}
	if _, ok := ts.held[pasta]; ok {
		t.Error("the canceled timer is still held")
	}
	if _, ok := ts.held[eggs]; !ok {
		t.Error("cancelling one timer took another with it")
	}

	if err := ts.cancelFromHA("all"); err != nil {
		t.Fatal(err)
	}
	if len(ts.held) != 1 || ts.held["kettle"] == nil {
		t.Errorf("after all, want only Home Assistant's kettle left, holding %v", ts.held)
	}
}
