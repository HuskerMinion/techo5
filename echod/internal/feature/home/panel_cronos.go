//go:build !dot && !spot

package home

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The panel's own size, which is what camera frames and slideshow photos are scaled to fit. Both
// Show 5 generations are 960x480; the Show 8 is 1280x800, and a photo fetched at the Show 5's size
// and drawn on it would be soft and letterboxed.
//
// Variables rather than constants because one build serves all three screens and the board is only
// known at run time. That is also why this file no longer covers the Dot: the board predicates only
// exist where there is a board to tell apart, and the Dot keeps its own copy in panel_dot.go.
var (
	// cameraFrameW and H are the size frames are scaled to fit.
	cameraFrameW, cameraFrameH = panelSize()

	// slideshowW and H are the panel's own size, the same thing by a different name.
	slideshowW, slideshowH = panelSize()
)

func panelSize() (w, h int) {
	if layout.Crown() {
		return 1280, 800
	}
	return 960, 480
}

// localCameraName is the device's own camera on the list, and what "show …" matches.
const localCameraName = "This Show"
