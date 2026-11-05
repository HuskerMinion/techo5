package diag

import "testing"

// A Snapcast server named on the home network is taken out of a bundle, port or not.
func TestSnapcastHost(t *testing.T) {
	for in, want := range map[string]string{
		"":                     "",
		"snapserver.home":      "snapserver.home",
		"snapserver.home:1704": "snapserver.home",
		" 192.168.1.20:1704 ":  "192.168.1.20",
	} {
		if got := snapcastHost(in); got != want {
			t.Errorf("snapcastHost(%q) = %q, want %q", in, got, want)
		}
	}
}
