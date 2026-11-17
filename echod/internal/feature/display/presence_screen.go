//go:build !dot

package display

import (
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/presence"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
)

// The screen and the room (feature/presence): with presence detection on and a wait set, the screen
// goes dark once nobody has been near for that long, and lights again when somebody comes near. Never
// at night (the night has its own rules), never while something is going on (a turn, a call, a ring,
// the settings open), and never while the camera cannot see, since then an empty room is not known.
// Being spoken to lights it too: whoever said the wake word may be out of the camera's sight, across
// the room or off to one side, and wants to see the answer (techo5-spot#12). Once the turn is over it
// gets its full wait again before the empty room puts it out.
//
// Screen off after idle (idle_off.go) puts it out the same way without the camera: once nobody has
// touched it, spoken to it or, with presence detection on, been seen near it for its wait. A tap, the
// wake word or somebody near lights it again.

var awayDark struct {
	mu   sync.Mutex
	dark bool // the screen is out for the empty room or for going unused, and nobody else has lit it since
	idle bool // going unused put it out (Screen off after idle), not the empty room
	// wasOn is the screen having been lit at the last tick.
	wasOn bool
	// litAt is when it was last lit, by anything: it stays lit at least the room's wait from then.
	litAt time.Time
	// usedAt is when it was last lit, busy or had somebody near; a finger on it is the display's
	// touchedAt. Screen off after idle waits from the later of the two.
	usedAt time.Time
}

// awayTick puts the screen out or lights it for the room or for going unused, and reports whether it
// changed it. spoken is a turn going on (the wake word heard, an answer being given).
func (d *Display) awayTick(now time.Time, on, busy, spoken, night bool) bool {
	after := presence.ScreenOffAfter()
	idleAfter := idleOffAfter()
	near := presence.Present()
	d.mu.Lock()
	touched := d.touchedAt
	d.mu.Unlock()
	awayDark.mu.Lock()
	dark, idle := awayDark.dark, awayDark.idle
	if on && (!awayDark.wasOn || spoken) {
		awayDark.litAt = now // a turn keeps it lit: the wait starts again when the turn is over
	}
	if on && (!awayDark.wasOn || busy || near) {
		awayDark.usedAt = now
	}
	awayDark.wasOn = on
	litFor := now.Sub(awayDark.litAt)
	used := awayDark.usedAt
	awayDark.mu.Unlock()
	if touched.After(used) {
		used = touched
	}
	set := func(dark, idle bool) {
		awayDark.mu.Lock()
		awayDark.dark, awayDark.idle = dark, idle
		awayDark.mu.Unlock()
	}
	// What lights a screen out for the empty room: somebody near, or the room's screen-off gone (turned
	// off, presence off, the camera long stopped). One out for going unused needs no camera to light
	// again: somebody seen near does it, or the setting turned to never; otherwise a tap or a turn.
	back := after == 0 || near
	if idle {
		back = idleAfter == 0 || near
	}
	switch {
	case dark && on:
		// Lit by something else since: a touch, a ring, Home Assistant. The room no longer owns it.
		set(false, false)
	case dark && !on && !night && spoken:
		// Ahead of a blind camera too: the shutter closed leaves the microphones listening.
		slog.Info("screen: spoken to, on again")
		set(false, false)
		d.apply(true, d.ceilingOrDefault(), false)
		return true
	case dark && !on && !idle && presence.Blind():
		// The camera stopped (the mute button, the shutter) with the screen out for an empty room: it
		// is left as it is, for a touch or the camera's return to light.
	case dark && !on && !night && back:
		if near {
			slog.Info("screen: somebody near, on again")
		} else {
			slog.Info("screen: no longer put out, on again")
		}
		set(false, false)
		d.apply(true, d.ceilingOrDefault(), false)
		return true
	case !dark && on && !night && !busy:
		// Lit a moment ago (a touch, a ring) with nobody seen since: it gets the room's full wait.
		if after > 0 && litFor >= after && presence.EmptyFor(now) >= after {
			slog.Info("screen: nobody near, off", "after", after)
			set(true, false)
			d.apply(false, d.ceilingOrDefault(), false)
			return true
		}
		if idleAfter > 0 && now.Sub(used) >= idleAfter {
			slog.Info("screen: unused, off", "after", idleAfter)
			set(true, true)
			d.apply(false, d.ceilingOrDefault(), false)
			return true
		}
	}
	return false
}

// watchRoom wakes the frame loop when somebody comes or goes, so a dark screen lights at once.
func (d *Display) watchRoom() {
	presence.Get().Changed.Listen(func(struct{}) { d.wake() })
	// A hand held over the camera stops whatever is ringing, as the stop word or a tap would.
	presence.Get().Gesture.Listen(func(name string) {
		if name == "cover" && ring.End() {
			d.wake()
		}
	})
}
