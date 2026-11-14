//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A finger held on the plain clock tells Home Assistant while the switch is on, and only then.
func TestAHoldOnTheClockIsTold(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	var told []string
	was := fireGesture
	fireGesture = func(name string) { told = append(told, name) }
	t.Cleanup(func() { fireGesture = was })

	d := &Display{}
	d.view.Phase = "idle"
	d.clockHoldScene(true)
	d.clockHeld()
	if len(told) != 0 {
		t.Errorf("told %v with the switch off", told)
	}
	if err := config.Set().Screen().ClockHold(true); err != nil {
		t.Fatal(err)
	}
	d.clockHoldScene(false) // a page over the clock
	d.clockHeld()
	if len(told) != 0 {
		t.Errorf("told %v with a page over the clock", told)
	}
	d.clockHoldScene(true)
	if !d.clockHolds {
		t.Error("the plain clock with the switch on does not ask for holds")
	}
	d.clockHeld()
	if len(told) != 1 || told[0] != "hold" {
		t.Errorf("told %v, want one hold", told)
	}
}

// Over the screensaver, a card or a popup, the clock is not the plain clock, and a hold there is not
// told.
func TestOnlyThePlainClockTakesHolds(t *testing.T) {
	if !plainClock(&scene{}, true) {
		t.Fatal("the plain clock does not take holds")
	}
	for name, s := range map[string]scene{
		"the screensaver":  {slideshowScreensaver: image.NewRGBA(image.Rect(0, 0, 1, 1))},
		"a notification":   {showNotification: true},
		"a reminder":       {showReminder: true},
		"an announcement":  {showAnnouncement: true},
		"the night clock":  {redClock: true},
		"the light before": {sunrise: 0.5},
	} {
		if plainClock(&s, true) {
			t.Errorf("%s takes holds", name)
		}
	}
	if plainClock(&scene{}, false) {
		t.Error("a page of its own takes holds")
	}
}

// While an announcement is recorded the strip's hold throws it away, so holds are reported: without
// them a long press lifted as a tap, and sent it.
func TestRecordingAnAnnouncementAsksForHolds(t *testing.T) {
	d := &Display{on: true}
	wanted := func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.holdsWanted()
	}
	if wanted() {
		t.Fatal("holds wanted with nothing up")
	}
	d.recordHoldScene(true)
	if !wanted() {
		t.Error("recording an announcement does not ask for holds")
	}
	d.recordHoldScene(false)
	if wanted() {
		t.Error("holds still wanted after the recording ended")
	}
}
