//go:build panel

package display

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/panelscreen"
)

// No app serves the screen in a test: every backlight and frame would wait out the whole dial.
func init() { panelscreen.DialWait = 0 }

// A panel's app updates it: the Updates row is a button only, with no Stable or Dev to pick.
func TestThePanelsUpdatesRowIsOnlyAButton(t *testing.T) {
	for _, r := range generalRows(sheetView{st: settings{version: "v0.1.1"}, now: time.Now()}) {
		if r.id != "updates" {
			continue
		}
		if r.kind != ctlButton || r.value != "" {
			t.Errorf("updates row: kind %v, value %q; want a button with no channel", r.kind, r.value)
		}
		return
	}
	t.Fatal("no Updates row")
}
