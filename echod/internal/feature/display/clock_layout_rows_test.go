//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Clock position, Weather position and Date color each open their list from the settings screen, and
// a choice there is kept. Weather position once had its list and no way to it: a tap did nothing.
func TestClockLayoutRowsOpenAndChoose(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, row := range clockLayoutRows() {
		p, ok := pickerFor(row.id, sheetView{})
		if !ok || len(p.opts) < 2 {
			t.Errorf("%s: no list (%+v)", row.id, p)
			continue
		}
		d := &Display{poke: make(chan struct{}, 1)}
		d.rowTap(row.id, partMain, 0)
		if d.picker != row.id {
			t.Errorf("%s: a tap opened %q", row.id, d.picker)
		}
		d.choose(row.id, 1)
		if p, _ := pickerFor(row.id, sheetView{}); p.cur != 1 {
			t.Errorf("%s: chose the second, the list has %d", row.id, p.cur)
		}
	}
	if !weatherOnRight() {
		t.Error("Weather position's second choice is not the top right")
	}
}
