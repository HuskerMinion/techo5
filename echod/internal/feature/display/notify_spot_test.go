//go:build spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// A card puts the menu away, whatever the hour: a menu left open would sit between the card and the
// tap that dismisses it.
func TestACardClosesTheMenuAtNight(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	notify.SetNight(func(time.Time) bool { return true })
	t.Cleanup(func() { notify.SetNight(func(time.Time) bool { return false }) })

	d := &Display{on: true, menuOpen: true, poke: make(chan struct{}, 1)}
	if err := notify.Get().Notify("The washer is done", "", "", "", "", 60); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { notify.Get().Dismiss() })
	d.notifyLights()
	if d.menuOpen {
		t.Error("the menu is still open under a card")
	}
}
