//go:build !panel

package home

import "testing"

// Off a panel the board's sizes stand whatever the screen says: SetPanelSize is for a panel's canvas.
func TestSetPanelSizeOnlyOnAPanel(t *testing.T) {
	SetPanelSize(1280, 800)
	if w, h := slideshowSize(); w != slideshowW || h != slideshowH {
		t.Errorf("photos are %dx%d, want the board's %dx%d", w, h, slideshowW, slideshowH)
	}
	if w, h := artSize(); w != artW || h != artH {
		t.Errorf("covers are %dx%d, want the board's %dx%d", w, h, artW, artH)
	}
}
