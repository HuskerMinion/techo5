package led

import (
	"math"
	"sync/atomic"
	"time"
)

// talker is where whoever is talking is, as the room's Facing puts it: a fraction clockwise round the
// ring from segment 0, and whether that is known. The microphones' owner hands it over (SetTalker),
// because unlike a room effect this one is played from a plain name: a wake word's effect is chosen
// from the light's list and started without a room to read.
var talker atomic.Pointer[func() (float64, bool)]

// SetTalker gives "Toward Talker" where to point. Nil, or never set, leaves it a plain glow.
func SetTalker(f func() (float64, bool)) {
	if f == nil {
		talker.Store(nil)
		return
	}
	talker.Store(&f)
}

// toward lights the whole ring with a bright arc toward whoever is talking, as Amazon's own ring does
// when it hears its name: it says which way the device is listening. Where that is not known the ring
// glows evenly, which is what a wake effect without a direction would show anyway.
//
// It takes the ring light's color and works out the arc from it (listeningColors): in the default blue
// that is Amazon's pairing, a deep blue ring with a cyan arc.
//
// The arc eases toward a new direction rather than jumping, as Room Compass does, so a talker moving
// while they speak turns it rather than flicking it about. It breathes a little, as Pulse does, so the
// ring reads as listening rather than as a light left on.
func toward(p Palette) Frame {
	const (
		// width is how wide the arc is, in segments.
		width = 1.8
		ease  = 0.25

		// period is one breath, and depth how much of the brightness it takes.
		period = 1600 * time.Millisecond
		depth  = 0.2
	)
	ring, arc := listeningColors(p)
	var at float64
	var known bool

	return func(elapsed time.Duration) []Color {
		breath := 1 - depth*(1+math.Cos(2*math.Pi*float64(elapsed%period)/float64(period)))/2

		if f := talker.Load(); f != nil {
			if want, ok := (*f)(); ok {
				want = math.Mod(math.Mod(want, 1)+1, 1) * Segments
				if !known {
					at, known = want, true
				} else {
					d := math.Mod(math.Mod(want-at, Segments)+Segments+Segments/2, Segments) - Segments/2
					at += d * ease
				}
			}
		}

		out := make([]Color, Segments)
		for i := range out {
			f := 0.0
			if known {
				if d := ringDist(i, at); d < width {
					f = (1 - d/width) * (1 - d/width)
				}
			} else {
				f = 0.3
			}
			out[i] = scale(blend(ring, arc, f), breath)
		}
		return out
	}
}

// listeningColors is the ring and the arc for a ring light of p's first color.
//
// The arc is the color lighter, nearly white, and its hue turned a sixth of the way round toward
// cyan's side of it; the ring is the color itself at under a third. Both are pushed that far because
// less did not read as pointing on the unit (2026-11-23: a 45 degree turn over a ring at three
// quarters looked like a lighter patch, not an arc): from the blue the ring is set to by default, a deep blue ring and a
// cyan arc, as Amazon's. Hue does the work rather than brightness, because the Dot 5's fabric spreads
// every module over its neighbours and evens brightness out (2026-11-23: a white ring with one module at
// full and the rest at a sixteenth looked even). A color with too little hue to turn (white, greys)
// takes the default blue pair, since there is nothing in it to make an arc from.
func listeningColors(p Palette) (ring, arc Color) {
	const (
		// shift is how far round the wheel the arc turns, lighten how much of its saturation it gives
		// up, and dim what the ring keeps of its value.
		shift   = -60.0 / 360
		lighten = 0.35
		dim     = 0.3
	)
	if len(p) > 1 {
		return p[0], p[len(p)-1]
	}
	h, s, v := toHSV(p[0])
	if s < 0.25 || v < 0.1 {
		return listening[0], listening[1]
	}
	return fromHSV(h, s, v*dim), fromHSV(h+shift, s*lighten, 1)
}
