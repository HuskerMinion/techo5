package display

import (
	"github.com/HuskerMinion/techo5/echod/internal/feature/babymon"
	"github.com/HuskerMinion/techo5/echod/internal/feature/camstream"
)

// watched is whether the screen says something is being sent out of the room: the camera, over the
// network (feature/camstream), or the room's sound to a browser (feature/babymon).
func watched() bool { return camstream.Watching() || babymon.Listening() }

// watchedLight is the backlight while the camera may be streamed: a dark panel keeps the dimmest light
// while anything watches (feature/camstream), so the dot that says so can be seen in the room. A lit
// panel's own light is left alone.
func watchedLight(target float64, on, watched bool, dimmest float64) float64 {
	if !on && watched && target < dimmest {
		return dimmest
	}
	return target
}
