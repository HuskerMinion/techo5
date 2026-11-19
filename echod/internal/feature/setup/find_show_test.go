//go:build !dot

package setup

import "testing"

// The cards a Dot leaves out are found where the build has them, so the Dot's test means something.
func TestFindHasWhatAShowHas(t *testing.T) {
	f, c := in(t)
	if names, _, _ := found(t, f, c, "dashboard server"); !has(names, "dashboard") {
		t.Errorf("a find for dashboard server found %v", names)
	}
}
