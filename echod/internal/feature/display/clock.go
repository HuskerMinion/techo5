package display

import (
	"sync/atomic"
	"time"
)

// clock24 is the screen showing 15:04 rather than 3:04 PM, and noAMPM a 12-hour clock face without
// its AM/PM. They live here so every page drawn reads them without taking the config's lock each frame.
var clock24, noAMPM atomic.Bool

// clockHM is the time as a clock face shows it, with its AM/PM set apart: 3:04, or 15:04.
func clockHM(t time.Time) string {
	if clock24.Load() {
		return t.Format("15:04")
	}
	return t.Format("3:04")
}

// clockSuffix is what goes beside or under clockHM: PM, or nothing on a 24-hour clock or a 12-hour one
// set to go without.
func clockSuffix(t time.Time) string {
	if clock24.Load() || noAMPM.Load() {
		return ""
	}
	return t.Format("PM")
}

// clockText is a time within a line of text: 3:04 PM, or 15:04.
func clockText(t time.Time) string {
	if clock24.Load() {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}
