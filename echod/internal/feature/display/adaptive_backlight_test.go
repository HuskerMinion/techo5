//go:build !dot && !spot

package display

import (
	"slices"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"
)

// Adaptive backlight is offered only while the GPU draws the panel: on the cores there is nothing to
// draw the pixels brighter with.
func TestAdaptiveBacklightOnlyWhereTheGPUDraws(t *testing.T) {
	defer func(f func() bool) { liftOffered = f }(liftOffered)
	d := Get()
	for _, gpu := range []bool{false, true} {
		liftOffered = func() bool { return gpu }
		has := slices.Contains(d.Entities(), esphome.Entity(d.liftSw))
		if has != gpu {
			t.Errorf("GPU drawing %v: Adaptive backlight offered %v", gpu, has)
		}
	}
}
