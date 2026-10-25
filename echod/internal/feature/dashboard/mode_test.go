//go:build !dot

package dashboard

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Tessera restored or imported on a device that has no Tessera - a Spot, a panel - is Off everywhere,
// not only in Home Assistant: the page must not take it for a streamed dashboard with no stream, nor
// draw the Tessera page without the feature, and Dashboard when idle has nothing to stand in.
func TestAModeTheDeviceDoesNotHaveIsOff(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Mode(config.DashboardTessera); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Dashboard().Idle(true); err != nil {
		t.Fatal(err)
	}
	f := Get()
	f.Restore(config.Get())

	want, idle := config.DashboardOff, false
	if slices.Contains(Modes(), config.DashboardTessera) {
		want, idle = config.DashboardTessera, true // the Show has it
	}
	if got := f.Mode(); got != want {
		t.Errorf("Mode() = %q, want %q", got, want)
	}
	if got := f.Idle(); got != idle {
		t.Errorf("Idle() = %v, want %v", got, idle)
	}
	if got := f.mode.Get(); got != want.Label() {
		t.Errorf("Home Assistant shows %q, want %q", got, want.Label())
	}
}
