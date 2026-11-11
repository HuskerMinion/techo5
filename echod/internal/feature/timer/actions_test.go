package timer

import (
	"strings"
	"testing"
	"time"
)

func TestHomeAssistantCancelsOnlyTheDevicesOwnTimers(t *testing.T) {
	ts := build()
	ts.Event(started("kettle", 180)) // Home Assistant's, set by voice
	pasta := ts.Start("Pasta", 10*time.Minute)
	eggs := ts.Start("Eggs", 7*time.Minute)

	if err := ts.cancelFromHA("kettle"); err == nil {
		t.Error("canceling Home Assistant's own timer from here should be refused")
	}
	if err := ts.cancelFromHA("local:nope"); err == nil {
		t.Error("canceling a timer that does not exist should fail")
	}
	if err := ts.cancelFromHA(pasta); err != nil {
		t.Fatalf("canceling %s: %v", pasta, err)
	}
	if _, ok := ts.held[pasta]; ok {
		t.Error("the canceled timer is still held")
	}
	if _, ok := ts.held[eggs]; !ok {
		t.Error("canceling one timer took another with it")
	}

	if err := ts.cancelFromHA("all"); err != nil {
		t.Fatal(err)
	}
	if len(ts.held) != 1 || ts.held["kettle"] == nil {
		t.Errorf("after all, want only Home Assistant's kettle left, holding %v", ts.held)
	}
}

// A timer of the device's own can be canceled by its label too, for a caller that has the label and not
// the id. Two with the same label are not guessed between, and Home Assistant's are still refused.
func TestHomeAssistantCancelsTheDevicesOwnTimerByItsLabel(t *testing.T) {
	ts := build()
	ts.Event(started("kettle", 180))
	wait := ts.StartSilent("Mira wartet", time.Minute, countdownColor)
	tea := ts.Start("Tea", 3*time.Minute)
	ts.Start("Tea", 4*time.Minute)
	t.Cleanup(func() { _ = ts.cancelFromHA("all") })

	if err := ts.cancelFromHA("mira WARTET"); err != nil {
		t.Fatalf("canceling by label: %v", err)
	}
	if _, ok := ts.held[wait]; ok {
		t.Error("the timer canceled by its label is still held")
	}
	if err := ts.cancelFromHA("Tea"); err == nil || !strings.Contains(err.Error(), tea) {
		t.Errorf("two timers called Tea: err = %v, want one naming their ids", err)
	}
	if len(ts.held) != 3 {
		t.Errorf("holding %d timers, want the kettle and both teas", len(ts.held))
	}
	if err := ts.cancelFromHA("kettle"); err == nil {
		t.Error("canceling Home Assistant's own timer from here should be refused")
	}
}
