//go:build !dot && !spot

package display

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// A card that navigates to #techo5-alarms opens the settings on Alarms & Timers (#651), over the
// dashboard, as a card navigating to #techo5-library opens the library.
func TestTheAlarmsTileOpensTheAlarms(t *testing.T) {
	d := &Display{poke: make(chan struct{}, 1)}
	d.dash = true
	d.tileAction(&dashTile{action: &dashboard.Action{View: dashboard.AlarmsPath}})
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.sheet || d.cat != catAlarms || d.draft != nil {
		t.Errorf("sheet %v on %v, draft %v", d.sheet, d.cat, d.draft)
	}
}
