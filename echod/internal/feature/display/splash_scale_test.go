//go:build !dot && !spot

package display

import "testing"

// The mark keeps the Shows' size on a Show and grows with the panel on a board that draws it in
// proportion.
func TestABoardDrawsTheSplashInProportion(t *testing.T) {
	defer func(f func() bool) { splashInProportion = f }(splashInProportion)
	splashInProportion = func() bool { return false }
	if got := splashScale(2 * drawnFor); got != logoScale {
		t.Errorf("a Show's mark at %v, want logoScale %v", got, logoScale)
	}
	splashInProportion = func() bool { return true }
	if got := splashScale(2 * drawnFor); got != 2*logoScale {
		t.Errorf("a mark in proportion at %v, want %v", got, 2*logoScale)
	}
}
