//go:build !dot && !spot

package home

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The panel's own size, which is what camera frames and slideshow photos are scaled to fit. Both
// Show 5 generations are 960x480; both Show 8 generations are 1280x800, and a photo fetched at the Show 5's size
// and drawn on it would be soft and letterboxed.
//
// Variables rather than constants because one build serves all four screens and the board is only
// known at run time. That is also why this file no longer covers the Dot: the board predicates only
// exist where there is a board to tell apart, and the Dot keeps its own copy in panel_dot.go. A TECHO5
// Panel has no board to go by: these are only where it starts, until its screen says its own size
// (SetPanelSize, size.go). Read them through slideshowSize and the rest there.
var (
	// cameraFrameW and H are the size frames are scaled to fit.
	cameraFrameW, cameraFrameH = panelSize()

	// slideshowW and H are the panel's own size, the same thing by a different name.
	slideshowW, slideshowH = panelSize()

	// radarW and H are the size the rain map is composited at. The radar page draws it one to one,
	// so this has to be the panel or the map does not fill the screen.
	radarW, radarH = panelSize()

	// artW and artH are the size a now-playing background picture is fitted to. The page draws it at
	// one to one, so this has to be the panel.
	artW, artH = panelSize()
)

// roundPanel is whether the screen is a circle in its square: every Show's is a rectangle.
const roundPanel = false

func panelSize() (w, h int) {
	switch {
	case layout.Skylight():
		// The Skylight Calendar 15: a 1920x1080 landscape panel.
		return 1920, 1080
	case layout.Show8():
		return 1280, 800
	}
	return 960, 480
}

// localCameraName is the device's own camera on the list, and what "show …" matches.
const localCameraName = "This Show"
