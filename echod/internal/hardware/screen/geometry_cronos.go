//go:build !dot && !spot

package screen

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// rotated: the Echo Show 5's panel is portrait and the device sits landscape. The Skylight's
// 1920×1080 panel is landscape already and is drawn as it is.
var rotated = !layout.Skylight()
