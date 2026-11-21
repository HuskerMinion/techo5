//go:build spot

package display

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// A card that navigates to #techo5-alarms opens the settings on Alarms & Timers (#651), as on the Show.
func TestTheAlarmsTileOpensTheAlarms(t *testing.T) {
	d := &Display{poke: make(chan struct{}, 1)}
	d.tileAction(&dashTile{action: &dashboard.Action{View: dashboard.AlarmsPath}})
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.sheetOpen || d.sheetGrid || d.cat != catAlarms {
		t.Errorf("sheet %v, on the six %v, on %v", d.sheetOpen, d.sheetGrid, d.cat)
	}
}
