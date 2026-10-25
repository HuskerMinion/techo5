//go:build !dot && !spot

package display

import (
	fcamstream "github.com/HuskerMinion/techo5/echod/internal/feature/camstream"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The ports the camera is served on, for the camera test to name: the stream's, and the web port, which
// has the pages and ONVIF.
const (
	camTestRTSPPort = fcamstream.Port
	camTestWebPort  = web.Port
)

// camTestPath is how this board makes its camera's pictures, as the camera test says it: the sensor and
// what develops its frames, and what makes the pages' JPEGs, a paragraph each.
func camTestPath() []string {
	var path []string
	switch {
	case layout.AthenaMainline():
		path = append(path, "OV13855, RAW10 2112x1568, 30 fps → developed on the cores")
	case layout.Board == "cronos":
		path = append(path, "OV02B10, RAW10 1600x1200 → developed on the cores")
	case layout.Checkers() || layout.Crown():
		path = append(path, "OV9734, RAW10 1280x720 → developed on the cores")
	default:
		path = append(path, "Sensor → developed on the cores")
	}
	return append(path, "Pages: JPEGs developed and encoded on the cores")
}
