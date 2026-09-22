//go:build dot

package home

// The Dot has no screen, so nothing here is ever read: hasScreen is false and every path that would
// scale a camera frame or a photo to a panel is unreachable. These exist so the shared code in this
// package compiles, and they keep the Show 5's numbers because that is where they came from.
const (
	// cameraFrameW and H are the size frames are scaled to fit.
	cameraFrameW = 960
	cameraFrameH = 480

	// localCameraName is the device's own camera on the list, and what "show …" matches.
	localCameraName = "This Show"

	// slideshowW and H are the panel's own size, the same thing by a different name.
	slideshowW = 960
	slideshowH = 480
)
