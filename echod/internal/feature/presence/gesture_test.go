//go:build !dot

package presence

import (
	"math/rand/v2"
	"testing"
)

// A palm on the lens (dark and smooth for a second, then the room again) is a cover; a dark room
// (grainy), a cover held too long, and a flicker too short to be a hand are not.
func TestACoverIsAPalmOnTheLens(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 10))
	palm := func() []uint8 {
		g := make([]uint8, gridW*gridH)
		for i := range g {
			g[i] = uint8(12 + i%gridW/8 + r.IntN(2)) // dark, a gentle slope, almost no grain
		}
		return g
	}
	dark := func() []uint8 {
		g := make([]uint8, gridW*gridH)
		for i := range g {
			g[i] = uint8(12 + r.IntN(18)) // dark and grainy, as measured (0.26-0.32): a room with the light off
		}
		return g
	}
	run := func(middle func() []uint8, frames int) bool {
		var c coverDetector
		t0, seen := 100.0, false
		for k := range 16 {
			seen = c.step(room(r, -1, 0), t0+float64(k)/8) || seen
		}
		t0 += 2
		for k := range frames {
			seen = c.step(middle(), t0+float64(k)/8) || seen
		}
		t0 += float64(frames) / 8
		for k := range 8 {
			seen = c.step(room(r, -1, 0), t0+float64(k)/8) || seen
		}
		return seen
	}
	if !run(palm, 8) {
		t.Error("a palm for a second was not seen")
	}
	if run(dark, 8) {
		t.Error("a dark room was taken for a palm")
	}
	if run(palm, 2) {
		t.Error("a quarter of a second was taken for a palm")
	}
	if run(palm, 48) {
		t.Error("six seconds was taken for a palm")
	}
}
