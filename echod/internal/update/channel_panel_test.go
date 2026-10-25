//go:build panel

package update

import "testing"

// A panel has no release stream of its own and never asks for a manifest: its app updates it.
func TestAPanelFollowsNoChannel(t *testing.T) {
	if Published() {
		t.Fatal("a panel says it has releases to check")
	}
	for _, c := range Channels() {
		if u := c.URL(); u != "" {
			t.Errorf("%s: %s", c.Label(), u)
		}
	}
}
