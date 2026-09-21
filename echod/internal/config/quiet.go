package config

import (
	"fmt"
	"time"
)

// Quiet hours: the device makes no sound of its own between these hours.
//
// What it silences is what the device starts by itself — an announcement from another device in the
// house, the tone it makes when it hears its name, the noises it makes about itself. What it does
// not silence is anything somebody asked for: an alarm, a timer, a phone ringing, a reply to a
// question, or anything Home Assistant was told to say. A quiet hour that swallowed an alarm would
// be a broken alarm clock, and one that swallowed a doorbell announcement would be worse.

// QuietHours is the window, as "22-7" — from ten at night to seven in the morning — or empty for
// never. The same shape as the screen's night hours, and usually the same hours.
func (s Speaker) Quiet(now time.Time) bool { return inWindow(s.QuietHours, now) }

// inWindow is whether now falls inside a "from-to" window of whole hours, which may cross midnight.
func inWindow(v string, now time.Time) bool {
	var from, to int
	if _, err := fmt.Sscanf(v, "%d-%d", &from, &to); err != nil {
		return false
	}
	if from < 0 || from > 23 || to < 0 || to > 23 || from == to {
		return false
	}
	h := now.Hour()
	if from < to {
		return h >= from && h < to
	}
	return h >= from || h < to
}

// Quiet is whether the device should keep to itself now.
func Quiet() bool { return Get().Speaker.Quiet(time.Now()) }
