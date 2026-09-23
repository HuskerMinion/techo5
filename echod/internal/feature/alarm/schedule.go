package alarm

import (
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// source is one thing that can ring: an alarm set on the device, a Home Assistant helper, or a snooze.
type source struct {
	key   string // stable across rebuilds: the alarm's ID, the helper's entity, "snooze:" + the original
	label string
	hour  int
	min   int
	days  uint8     // repeating weekdays; with none it rings once, at once if set, else the next hour:min
	once  time.Time // a fixed moment, for helpers with a date and for snoozes
	local bool      // set on the device, so ringing a one-off turns it off
}

// next is the first time at or after from that s rings, and false if it never will again.
func (s source) next(from time.Time) (time.Time, bool) {
	if !s.once.IsZero() {
		return s.once, !s.once.Before(from)
	}
	day := time.Date(from.Year(), from.Month(), from.Day(), s.hour, s.min, 0, 0, from.Location())
	for d := 0; d < 8; d++ {
		at := day.AddDate(0, 0, d)
		if at.Before(from) {
			continue
		}
		if s.days == config.DaysOnce || s.days&(1<<at.Weekday()) != 0 {
			return at, true
		}
	}
	return time.Time{}, false
}

// stale is how late a ring may still happen: a device that was off, or whose clock jumped forward
// when NTP set it, rings what it missed only if it missed it by this little.
const stale = 10 * time.Minute

// due is the sources that ring in (last, now]: the window since the scheduler last looked.
func due(sources []source, last, now time.Time) []source {
	var out []source
	for _, s := range sources {
		at, ok := s.next(last.Add(time.Nanosecond))
		if !ok || at.After(now) || now.Sub(at) > stale {
			continue
		}
		out = append(out, s)
	}
	return out
}

// splitSnoozes divides snoozes into those still to come and those whose moment has gone by without
// them ringing.
//
// A one-off whose time is past is not merely late, it is inert: next reports nothing for it ever
// again, so due skips it, soonest skips it, and nothing removes it. It used to stay in the list for
// good while the settings sheet drew "Snoozed until" and a time that had been and gone.
//
// Being late by itself is not the fault — due may ring something up to stale minutes late, and that
// one prunes itself by firing. This runs after the firing, so what is left has genuinely missed.
func splitSnoozes(snoozed []source, now time.Time) (live, missed []source) {
	for _, s := range snoozed {
		if s.once.IsZero() || s.once.After(now) {
			live = append(live, s)
			continue
		}
		missed = append(missed, s)
	}
	return live, missed
}

// soonest is the next ring among sources after now.
func soonest(sources []source, now time.Time) (source, time.Time, bool) {
	var best source
	var bestAt time.Time
	found := false
	for _, s := range sources {
		at, ok := s.next(now)
		if !ok {
			continue
		}
		if !found || at.Before(bestAt) {
			best, bestAt, found = s, at, true
		}
	}
	return best, bestAt, found
}

// fromHelper reads an input_datetime's state: "07:30:00" rings every day, "2026-09-17 07:30:00" once.
func fromHelper(entity, state string, loc *time.Location) (source, bool) {
	state = strings.TrimSpace(state)
	s := source{key: entity}
	if t, err := time.ParseInLocation("15:04:05", state, loc); err == nil {
		s.hour, s.min, s.days = t.Hour(), t.Minute(), config.DaysEvery
		return s, true
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", state, loc); err == nil {
		s.once = t
		return s, true
	}
	return source{}, false
}

// clockSet reports whether the wall clock can be believed: before NTP has run the RTC can be years
// out, and an alarm must not ring for a morning that is not today.
func clockSet(now time.Time) bool { return now.Year() >= 2025 }
