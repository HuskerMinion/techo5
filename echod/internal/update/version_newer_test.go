package update

import "testing"

// Only something newer is offered: an older release would be refused by Install, and a version that
// cannot be ranked is left to whoever presses Install.
func TestNewer(t *testing.T) {
	for _, c := range []struct {
		offered, running string
		want             bool
	}{
		{"v0.9.10", "v0.9.9", true},
		{"v0.9.9", "v0.9.10", false},
		{"v0.9.9", "v0.9.9", false},
		{"v1.0.0-rc.1", "v0.9.10", true},
		{"v0.9.10", "v1.0.0-rc.1", false},
		{"v0.9.9", "family-test", true},
	} {
		if got := Newer(c.offered, c.running); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.offered, c.running, got)
		}
	}
}
