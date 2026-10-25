//go:build !dot

package display

import (
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
)

// The Connections card has a Secure Connections switch under Bluetooth wherever Bluetooth is up and
// the device lets it be changed, on unless turned off, saying what changing it costs (#183).
func TestSecureConnectionsRow(t *testing.T) {
	find := func(rows []settingRow) (settingRow, bool) {
		for _, r := range rows {
			if r.id == "btsc" {
				return r, true
			}
		}
		return settingRow{}, false
	}
	up := btaudio.State{Available: true}
	r, ok := find(connectionRows(sheetView{bt: up}))
	if !ok || r.kind != ctlToggle || !r.on || r.label != "Bluetooth Secure Connections" || !strings.Contains(r.sub, "Drops devices") {
		t.Errorf("on: %+v %v", r, ok)
	}
	if r, ok := find(connectionRows(sheetView{bt: up, st: settings{btSCOff: true}})); !ok || r.on {
		t.Errorf("off: %+v %v", r, ok)
	}
	if _, ok := find(connectionRows(sheetView{})); ok {
		t.Error("offered with no Bluetooth")
	}
	if _, ok := find(connectionRows(sheetView{bt: up, st: settings{btSCFixed: true}})); ok {
		t.Error("offered where it is fixed")
	}
}
