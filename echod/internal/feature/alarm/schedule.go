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

	// remind is a reminder, said once rather than rung, and ringOn the other devices it goes to.
	remind bool
	ringOn []string

	// sunrise is how many minutes of light come before it, nothing for none. lit is a snooze of an
	// alarm that had a wake light: it has no window of its own, but holds the light at full until it
	// rings (sunrise.go).
	sunrise int
	lit     bool

	// silent goes off without a sound, as an event for Home Assistant only.
	silent bool

	// lightOnly is a wake light and no more: at its time the night ends, without a sound.
	lightOnly bool

	// skips are the occurrences of this alarm left out, by exact moment. They belong to an alarm set
	// on the device that repeats: leaving one out neither turns it off nor changes its days, so its
	// next occurrence after the marked one is found as usual. They are kept until the alarm is
	// deleted, their moment behind them included (skip.go), so a search may have to step over
	// several.
	skips []time.Time
}

// next is the first time at or after from that s rings, and false if it never will again. An
// occurrence marked as left out is stepped over, and the search looks far enough for the one after
// it: a mark hides at most one matching day, so one match past the number of marks is the furthest
// the first one not marked can be, which for a weekly alarm is a week per mark. The marks are what a
// device left out and keeps until the alarm goes, so the count is its own history; the cap is only so
// that no file, however strange, is walked without an end.
func (s source) next(from time.Time) (time.Time, bool) {
	if !s.once.IsZero() {
		return s.once, !s.once.Before(from) && !s.skipped(s.once)
	}
	day := time.Date(from.Year(), from.Month(), from.Day(), s.hour, s.min, 0, 0, from.Location())
	for d, days := 0, min(weekPerMark*(len(s.skips)+1)+1, nextDays); d <= days; d++ {
		at := day.AddDate(0, 0, d)
		if at.Before(from) {
			continue
		}
		if s.days != config.DaysOnce && s.days&(1<<at.Weekday()) == 0 {
			continue
		}
		if s.skipped(at) {
			// A one-off has this occurrence only; stepping over it must not make another day.
			if s.days == config.DaysOnce {
				return time.Time{}, false
			}
			continue
		}
		return at, true
	}
	return time.Time{}, false
}

const (
	// weekPerMark is how many days one mark may put between two matching days: a week, for the widest
	// an alarm's days can be apart.
	weekPerMark = 7

	// nextDays caps source.next: a year and more, which is further than a week, so a weekly alarm
	// whose next occurrence is left out still finds the one after it, and further than the marks a
	// device can hold — only the occurrence whose window is open can be left out, so there is at most
	// one ahead. The cap is only so that a strange file cannot make a look walk without an end.
	nextDays = 400
)

// skipped is whether at is one of the occurrences left out for this source.
func (s source) skipped(at time.Time) bool {
	for _, x := range s.skips {
		if x.Equal(at) {
			return true
		}
	}
	return false
}

// skipMoments is the moments left out for the alarm with this ID, for a source to step over.
func skipMoments(skipped []config.Skip, id string) []time.Time {
	var out []time.Time
	for _, s := range skipped {
		if s.ID == id {
			out = append(out, s.At)
		}
	}
	return out
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
