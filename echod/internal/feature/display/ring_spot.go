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
}

func (r ringing) any() bool { return r.timerOn || r.alarm != nil }

func ringingNow(now time.Time) ringing {
	var st ringing
	st.timer, st.timerOn = timer.Get().RingingName()
	st.alarm = alarm.Get().View(now).Ringing
	st.snoozeIn = config.Get().Alarms.Snooze()
	return st
}

// ringGesture is a finger on the ringing face, and reports whether it was one.
func (d *Display) ringGesture(g touch.Gesture) bool {
	st := ringingNow(time.Now())
	if !st.any() {
		return false
	}
	switch g.Kind {
	case touch.Tap:
		timer.Get().Stop()
		alarm.Get().Stop()
	case touch.SwipeLeft, touch.SwipeRight:
		if st.alarm != nil {
			alarm.Get().Snooze()
		}
		timer.Get().Stop()
	default:
		return false
	}
	d.wake()
	return true
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
	r.centred(r.label, clip(r.label, r, title, 330), 130, colRing)
	r.timeLine(s.now, 262)

	r.centred(r.title, "Tap to stop", 372, colText)
	if st.alarm != nil {
		r.centred(r.small, fmt.Sprintf("swipe to snooze %d min", st.snoozeIn), 410, colDim)
	}
}
