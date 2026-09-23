//go:build spot

package display

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// The ringing face, as the Show has it: a timer that is done or an alarm going off takes the whole
// screen and lights a dark one, since the face is how it is stopped. A tap stops it; a sideways swipe
// snoozes an alarm.

var colRing = color.RGBA{255, 176, 32, 255}

// ringing is what is sounding: the timer's name (empty when none), and the alarm (nil when none).
type ringing struct {
	timer    string
	timerOn  bool
	alarm    *alarm.Ring
	snoozeIn int // minutes

	// silenced is a ring a button press quieted, waiting to be told whether that meant snooze. A
	// silent ring wearing the ringing face looks like one that stopped, and it is not one.
	silenced bool
}

func (r ringing) any() bool { return r.timerOn || r.alarm != nil }

func ringingNow(now time.Time) ringing {
	var st ringing
	st.timer, st.timerOn = timer.Get().RingingName()
	st.alarm = alarm.Get().View(now).Ringing
	st.snoozeIn = config.Get().Alarms.Snooze()
	st.silenced = ring.Offered()
	return st
}

// ringGesture is a finger on the ringing face, and reports whether it was one.
func (d *Display) ringGesture(g touch.Gesture) bool {
	st := ringingNow(time.Now())
	if !st.any() {
		return false
	}
	stop, snooze := ringMeans(g.Kind)
	switch {
	case stop:
		ring.End()
	case snooze:
		// Accept snoozes what can be snoozed and stops the rest, which is a timer beside the alarm.
		ring.Accept()
	default:
		return false
	}
	d.wake()
	return true
}

// ringMeans says what a gesture means on the ringing face.
//
// Hold and Release are a slow tap. The face says "Tap to stop", and a press of 450 ms is a tap by
// any reading of it — but Hold was refused here and fell through to the tail of the gesture chain,
// which opens the ring menu, over the ringing face. So pressing slightly too long at a ringing alarm
// opened a menu instead of stopping it. Release is taken for the same reason: it is what arrives
// when the finger goes, and by then the ring is stopped, so this is a no-op that keeps it from
// falling through the same way.
func ringMeans(k touch.Kind) (stop, snooze bool) {
	switch k {
	case touch.Tap, touch.Hold, touch.Release:
		return true, false
	case touch.SwipeLeft, touch.SwipeRight:
		return false, true
	}
	return false, false
}

// ringLights brings a dark panel up for something that starts ringing.
func (d *Display) ringLights() {
	if ringingNow(time.Now()).any() {
		d.mu.Lock()
		on, open := d.on, d.menuOpen
		if open {
			d.closeMenu()
		}
		d.mu.Unlock()
		if !on {
			d.apply(true, d.ceilingOrDefault(), false)
		}
	}
	d.wake()
}

func (r *roundRenderer) ringFace(s roundScene) {
	st := s.ringing
	pulse := 0.4 + 0.6*math.Abs(math.Sin(float64(s.now.UnixMilli())/350))
	r.arc(rimIn, rimOut, 0, 2*math.Pi, fade(colRing, pulse))

	title := "ALARM"
	switch {
	case st.timerOn && st.alarm != nil:
		title = "TIMER AND ALARM"
	case st.timerOn:
		title = "TIMER DONE"
		if st.timer != "" {
			name := st.timer
			if !strings.Contains(strings.ToLower(name), "timer") {
				name += " timer"
			}
			title = strings.ToUpper(name) + " DONE"
		}
	case st.alarm != nil && st.alarm.Label != "":
		title = strings.ToUpper(st.alarm.Label)
	}
	r.centered(r.label, clip(r.label, r, title, 330), 130, colRing)
	r.timeLine(s.now, 262)

	// A silenced ring says so where it used to say what to do, because what to do has changed: the
	// noise is already gone and the only question left is whether it comes back.
	if st.silenced {
		r.centered(r.title, "Silenced", 372, colText)
		if st.alarm != nil {
			r.centered(r.small, fmt.Sprintf("press again to snooze %d min", st.snoozeIn), 410, colDim)
		}
		return
	}

	r.centered(r.title, "Tap to stop", 372, colText)
	if st.alarm != nil {
		r.centered(r.small, fmt.Sprintf("swipe to snooze %d min", st.snoozeIn), 410, colDim)
	}
}
