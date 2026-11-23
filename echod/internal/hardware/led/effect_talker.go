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

// toward lights the whole ring softly with a bright arc toward whoever is talking, as Amazon's own
// ring does when it hears its name: it says which way the device is listening. Where that is not known
// the ring glows evenly, which is what a wake effect without a direction would show anyway.
//
// The arc eases toward a new direction rather than jumping, as Room Compass does, so a talker moving
// while they speak turns it rather than flicking it about. It breathes a little, as Pulse does, so the
// ring reads as listening rather than as a light left on.
func toward(p Palette) Frame {
	const (
		// floor is how bright the rest of the ring is, and width how wide the arc, in segments.
		floor = 0.25
		width = 2.6
		ease  = 0.25

		// period is one breath, and depth how much of the brightness it takes.
		period = 1600 * time.Millisecond
		depth  = 0.2
	)
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
		if !known {
			for i := range out {
				out[i] = p.Shade(0, 0.7*breath)
			}
			return out
		}
		for i := range out {
			out[i] = p.Shade(0, floor*breath)
		}
		dot(out, at, p.Shade(0, (1-floor)*breath), width)
		return out
	}
}
