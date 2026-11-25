package diag

import (
	"math"
	"time"
)

// luxEvery is the least time between two lux readings sent as the light changes, and luxStep the
// change worth sending: a lamp switched on or off, a cloud, not every flicker of a reading. Between
// them, the diagnostics' own interval sends it as before.
//
// A light that keeps changing (a television in a dark room, a ceiling fan's shadow) would otherwise
// send a reading every luxEvery for as long as it went on. So once two readings have gone within
// luxBusyFor of each other, the next waits luxBusy: a lamp switched on in a room that has been steady
// is in Home Assistant within seconds, as it was, and a flickering room settles to a reading every
// quarter minute.
var (
	luxEvery   = 2 * time.Second
	luxBusy    = 15 * time.Second
	luxBusyFor = 30 * time.Second
)

const luxStep = 0.1 // of the last reading sent, and at least 1 lux

// luxGap is the least time after the last reading sent before the next may go, given the time
// between the last two sent (0 with fewer than two).
func luxGap(between time.Duration) time.Duration {
	if between > 0 && between < luxBusyFor {
		return luxBusy
	}
	return luxEvery
}

// luxWorthSending is whether a reading of now is worth sending after had: luxStep of had, and at
// least a lux, so a dark room's 0 to 2 goes and a bright room's flicker does not.
func luxWorthSending(had, now float64) bool {
	return math.Abs(now-had) >= max(1, luxStep*had)
}
