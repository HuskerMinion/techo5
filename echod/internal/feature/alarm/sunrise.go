package alarm

import (
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// Waking to light. For the minutes before an alarm, the device lights the room: on a screen the
// whole panel becomes a sunrise (feature/display), and on a device with a light ring and no screen
// the ring comes up instead — the same curve, the same colours, on the only lamp a Dot has.
//
// The alarm itself is untouched. This is the light before it.

// SunriseFloor is where the ramp starts, as a fraction of full: enough to see by in a dark room and
// not enough to wake anybody by itself.
const SunriseFloor = 0.02

// sunriseEvery is how often the ring is repainted while the light comes up. Slow enough to be
// nothing, often enough that the change is not a series of steps.
const sunriseEvery = 2 * time.Second

// SunriseProgress is how far into the light we are: 0 outside it, rising to 1 as the alarm's time
// arrives. A snoozed alarm does not light the room again — it was already light the first time.
func (a *Alarms) SunriseProgress(now time.Time) float64 {
	mins := config.Get().Alarms.SunriseMinutes
	if mins <= 0 {
		return 0
	}
	next := a.View(now).Next
	if next == nil || next.Snoozed {
		return 0
	}
	window := time.Duration(mins) * time.Minute
	left := next.At.Sub(now)
	if left <= 0 || left > window {
		return 0
	}
	return 1 - float64(left)/float64(window)
}

// SunriseLevel is the fraction of full brightness the ramp asks for, eased so most of the change
// happens near the end rather than the moment it starts.
func SunriseLevel(progress float64) float64 {
	if progress <= 0 {
		return 0
	}
	return SunriseFloor + (1-SunriseFloor)*math.Pow(math.Min(progress, 1), 2)
}

// sunriseColor is the ring's colour as the light comes up: the deep red of the first minutes, then
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
