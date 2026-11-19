//go:build !dot && !spot && !panel

package touch

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The ThinkSmart View stands the other way up from a Show 8: with the panel turned the Show's way the
// picture is upside down on its stand. The kernel turns the picture half a turn on the display plane
// (video=DSI-1:800x1280,rotate=180 on its command line, which DRM's fbdev emulation applies), so the
// touch readings are turned over to match.
func init() {
	if layout.ThinkSmart() {
		turnedOver = true
	}
}
