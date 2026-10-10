//go:build spot

package display

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// A card puts the menu away by day: a menu left open would sit between the card and the tap that
// dismisses it. At night the card waits for a touch, and the touch that releases it puts the menu away
// (TestTheTouchThatReleasesANotificationOnlyReleasesIt).
func TestACardClosesTheMenuByDay(t *testing.T) {
	d := spotNoticeDisplay(t, false)
	d.menuOpen = true
	if err := notify.Get().Notify("The washer is done", "", "", "", "", "", 60); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { notify.Get().Dismiss() })
	d.notifyLights()
	if d.menuOpen {
		t.Error("the menu is still open under a card")
	}
}
