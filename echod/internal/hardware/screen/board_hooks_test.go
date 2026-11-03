//go:build !dot && !panel

package screen

import "testing"

// A board's own backlight node comes first where it says ok, and the others are left as they were
// where it does not.
func TestABoardBacklightComesFirst(t *testing.T) {
	defer func(f func() (string, int, bool)) { boardBacklight = f }(boardBacklight)
	boardBacklight = nil
	wantPath, wantTop := backlightNode()

	boardBacklight = func() (string, int, bool) { return "/sys/class/backlight/fake/brightness", 254, true }
	if path, top := backlightNode(); path != "/sys/class/backlight/fake/brightness" || top != 254 {
		t.Errorf("backlightNode() = %q, %d; want the board's", path, top)
	}
	boardBacklight = func() (string, int, bool) { return "", 0, false }
	if path, top := backlightNode(); path != wantPath || top != wantTop {
		t.Errorf("backlightNode() = %q, %d; want %q, %d as with none", path, top, wantPath, wantTop)
	}
}

// On a board whose one page scans out as written, pan stays on page 0 and asks nothing of the
// driver; with more pages it pans as before.
func TestASinglePageBoardDoesNotPan(t *testing.T) {
	defer func(v bool) { singlePage = v }(singlePage)
	singlePage = true
	d := &Device{pages: 1}
	if err := d.pan(1); err != nil || d.page != 0 {
		t.Errorf("one page: pan(1) = %v, page %d; want page 0", err, d.page)
	}
	d = &Device{pages: 2}
	if err := d.pan(1); err != nil || d.page != 1 {
		t.Errorf("two pages: pan(1) = %v, page %d; want page 1", err, d.page)
	}
}
