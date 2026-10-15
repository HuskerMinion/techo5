//go:build spot

package touch

import "testing"

// rawFrame is a panel whose raw frame is the screen's own, as the Spot's nearly is.
func rawFrame() (rawW, rawH int, toRaw func(x, y int) (int, int)) {
	return Width, Height, func(x, y int) (int, int) { return x, y }
}

func TestSpotSwipeDirections(t *testing.T) { checkSwipeDirections(t) }
