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

var awayDark struct {
	mu    sync.Mutex
	dark  bool      // the screen is out because the room is empty, and nobody else has lit it since
	wasOn bool      // the screen was lit at the last tick
	litAt time.Time // when it was last lit, by anything: it stays lit at least the wait from then
}

// awayTick puts the screen out or lights it for the room, and reports whether it changed it.
func (d *Display) awayTick(now time.Time, on, busy, night bool) bool {
	after := presence.ScreenOffAfter()
	awayDark.mu.Lock()
	dark := awayDark.dark
	if on && !awayDark.wasOn {
		awayDark.litAt = now
	}
	awayDark.wasOn = on
	litFor := now.Sub(awayDark.litAt)
	awayDark.mu.Unlock()
	set := func(v bool) {
		awayDark.mu.Lock()
		awayDark.dark = v
		awayDark.mu.Unlock()
	}
	switch {
	case dark && on:
		// Lit by something else since: a touch, a ring, Home Assistant. The room no longer owns it.
		set(false)
	case dark && !on && presence.Blind():
		// The camera stopped (the mute button, the shutter) with the screen out for an empty room: it
		// is left as it is, for a touch or the camera's return to light.
	case dark && !on && !night && (after == 0 || presence.Present()):
		slog.Info("screen: somebody near, on again")
		set(false)
		d.apply(true, d.ceilingOrDefault(), false)
		return true
	case !dark && on && !night && !busy && after > 0 && presence.EmptyFor(now) >= after:
		if litFor < after {
			// Lit a moment ago (a touch, a ring) with nobody seen since: it gets its full wait.
			return false
		}
		slog.Info("screen: nobody near, off", "after", after)
		set(true)
		d.apply(false, d.ceilingOrDefault(), false)
		return true
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
