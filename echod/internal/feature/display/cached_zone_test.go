//go:build !dot

package display

import "testing"

// A zone is read once and kept; a name that isn't one is kept as not known.
func TestCachedZone(t *testing.T) {
	a, b := cachedZone("Asia/Tokyo"), cachedZone("Asia/Tokyo")
	if a == nil || a != b {
		t.Errorf("Asia/Tokyo: %v, then %v", a, b)
	}
	if cachedZone("Not/AZone") != nil || cachedZone("../etc/passwd") != nil {
		t.Error("a name that isn't a zone")
	}
}
