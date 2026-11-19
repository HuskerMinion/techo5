//go:build dot

package setup

import "testing"

// A Dot has no dashboard server and no tailnets: a find for them does not find their cards.
func TestFindLeavesOutWhatADotHasNot(t *testing.T) {
	f, c := in(t)
	for q, card := range map[string]string{"dashboard server": "dashboard", "tailscale": "tailscale", "netbird": "netbird"} {
		if names, _, _ := found(t, f, c, q); has(names, card) {
			t.Errorf("a find for %s on a Dot found its card", q)
		}
	}
}
