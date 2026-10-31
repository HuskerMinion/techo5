//go:build !dot && !spot

package setup

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The Dashboard section's form saves the tile layout when it is given, keeps it when the form leaves it
// out (a page from before the setting, or a script), and refuses a choice the page did not offer
// without losing the rest of the form.
func TestDashboardPanelLayout(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := dashboard.Get()
	form := func(layout ...string) url.Values {
		v := url.Values{"mode": {string(f.Mode())}, "back": {dashboard.Back()}, "tiles": {dashboard.Tiles()}}
		if len(layout) > 0 {
			v.Set("layout", layout[0])
		}
		return v
	}
	saved := func() string { return config.Get().Dashboard.Layout }

	if p := saveDashboardPanel(deckPost(form())); p != "" || saved() != "" {
		t.Fatalf("a form without a layout: %q, layout %q; want it saved and the layout as it was", p, saved())
	}
	if p := saveDashboardPanel(deckPost(form("Name below"))); p != "" || saved() != "below" {
		t.Fatalf("Name below: %q, layout %q", p, saved())
	}
	if p := saveDashboardPanel(deckPost(form())); p != "" || saved() != "below" {
		t.Errorf("a form without a layout after Name below: %q, layout %q; want it kept", p, saved())
	}
	if p := saveDashboardPanel(deckPost(form("Sideways"))); p == "" || saved() != "below" {
		t.Errorf("a layout the page did not offer: %q, layout %q; want it refused and the layout kept", p, saved())
	}
	if p := saveDashboardPanel(deckPost(form("Side by side"))); p != "" || saved() != "" {
		t.Errorf("Side by side: %q, layout %q", p, saved())
	}
}
