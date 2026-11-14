//go:build !dot

package display

import "testing"

// The button that ends this device's music says Stop; over another room's player, which it only puts
// away, it says Done.
func TestTheWayOutOfNowPlayingSaysWhatItDoes(t *testing.T) {
	was := musicFollowed
	t.Cleanup(func() { musicFollowed = was })
	musicFollowed = func() bool { return false }
	if got := musicEndWord(); got != tr("stop") {
		t.Errorf("over its own music the button says %q", got)
	}
	musicFollowed = func() bool { return true }
	if got := musicEndWord(); got != tr("done") {
		t.Errorf("over another room's player the button says %q", got)
	}
}
