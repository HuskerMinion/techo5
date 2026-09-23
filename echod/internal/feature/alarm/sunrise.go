package alarm

import (
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// Waking to light. For the minutes before an alarm, the device lights the room: on a screen the
// whole panel becomes a sunrise (feature/display), and on a device with a light ring and no screen
// the ring comes up instead — the same curve, the same colors, on the only lamp a Dot has.
//
// The alarm itself is untouched. This is the light before it.

// SunriseFloor is where the ramp starts, as a fraction of full: enough to see by in a dark room and
// not enough to wake anybody by itself.
const SunriseFloor = 0.02

// sunriseEvery is how often the ring is repainted while the light comes up. Slow enough to be
// nothing, often enough that the change is not a series of steps.
const sunriseEvery = 2 * time.Second

// SunriseProgress is how far into the light we are: 0 outside it, rising to 1 as an alarm's time
// arrives. Each alarm has its own window (config.Alarms.SunriseFor), so one with none does not light
// the room for another's sake, and where two windows meet the further along wins. A snoozed alarm
// does not light the room again — it was already light the first time — and a reminder never does.
func (a *Alarms) SunriseProgress(now time.Time) float64 {
	return sunriseAt(a.sources(now), now)
}

func sunriseAt(sources []source, now time.Time) float64 {
	best := 0.0
	for _, s := range sources {
		if s.sunrise <= 0 {
			continue
		}
		at, ok := s.next(now)
		if !ok {
			continue
		}
		window := time.Duration(s.sunrise) * time.Minute
		left := at.Sub(now)
		if left <= 0 || left > window {
			continue
		}
		best = max(best, 1-float64(left)/float64(window))
	}
	return best
}

// SunriseLevel is the fraction of full brightness the ramp asks for, eased so most of the change
// happens near the end rather than the moment it starts.
func SunriseLevel(progress float64) float64 {
	if progress <= 0 {
		return 0
	}
	return SunriseFloor + (1-SunriseFloor)*math.Pow(math.Min(progress, 1), 2)
}

// sunriseColor is the ring's color as the light comes up: the deep red of the first minutes, then
// orange, then a warm white, scaled by how far up it is.
func sunriseColor(progress float64) led.Color {
	lvl := SunriseLevel(progress)
	r := 255.0
	g := 40 + 200*math.Min(progress*1.15, 1)
	b := 10 + 180*math.Max(progress-0.45, 0)/0.55
	scale := func(v float64) uint8 { return uint8(math.Min(math.Max(v*lvl, 0), 255)) }
	return led.Color{R: scale(r), G: scale(g), B: scale(math.Min(b, 235))}
}

// sunriseRing paints the ring for the light before an alarm, and clears it when there is none. It
// reports whether the light is on, so the caller knows to come back sooner.
func (a *Alarms) sunriseRing(now time.Time) bool {
	p := a.SunriseProgress(now)
	if p <= 0 {
		if a.lighting {
			a.sun.Clear()
			a.lighting = false
		}
		return false
	}
	a.lighting = true
	a.sun.Paint(led.Solid(sunriseColor(p)))
	return true
}
