package timezone

import (
	"log/slog"
	"time"
)

// Home Assistant's time sets the clock when NTP cannot: a device kept off the internet never reaches an
// NTP server, and its clock stays wherever the RTC left it, often years back. NTP comes first: once
// it has the clock, Home Assistant's answer is only compared with it. The answer is in whole seconds,
// which is all a clock with nothing better needs.

// clockOff is how far the clock may be from Home Assistant's before it is set from it, while NTP has
// not set it: the answer is whole seconds and a moment late, so less would set a clock that is right.
const clockOff = 10 * time.Second

// Stand-ins for tests; the real ones are the system's (clock_dir.go, clock_panel.go).
var (
	clockNow    = time.Now
	clockSynced = ntpSynced
	clockSet    = setSystemClock
)

// takeTime sets the clock from Home Assistant's time, in seconds since 1970, when the clock needs it.
func takeTime(epoch uint32) {
	if epoch == 0 {
		return // an older Home Assistant sends none
	}
	ha := time.Unix(int64(epoch), 0)
	now := clockNow()
	if !needsClock(now, ha, clockSynced()) {
		return
	}
	if err := clockSet(ha); err != nil {
		slog.Warn("clock not set from home assistant", "err", err)
		return
	}
	slog.Info("clock set from home assistant", "was", now.UTC().Format(time.RFC3339), "now", ha.UTC().Format(time.RFC3339))
}

// needsClock is whether a clock reading now should take Home Assistant's time ha: one never set
// (before 2020, where boot leaves it until NTP answers), or one NTP has not set that is off by more
// than clockOff. A clock NTP keeps is left alone, and so is a Home Assistant whose own clock was
// never set.
func needsClock(now, ha time.Time, synced bool) bool {
	if ha.Year() < 2020 {
		return false
	}
	if now.Year() < 2020 {
		return true
	}
	off := now.Sub(ha)
	return !synced && (off > clockOff || off < -clockOff)
}
