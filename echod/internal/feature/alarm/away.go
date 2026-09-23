package alarm

import (
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
)

const (
	// ResumeWithin is how late a ring may still sound when the device comes back. An update's
	// restart at 6:29 must not lose a 6:30 alarm, and a device that crashes while ringing must not
	// scream every time it comes back up either: past this, what fell due is said, not rung.
	ResumeWithin = 2 * time.Minute

	// aliveEvery is how often the device writes down that it is running. The gap it leaves is how
	// long a ring can be taken for missed when it was not, so every ring writes one too.
	aliveEvery = 10 * time.Minute
)

// fellDue is a source and the last time it fell due in some window.
type fellDue struct {
	s  source
	at time.Time
}

// latestIn is the last time s fell due in (from, to], and false if it did not. The latest rather
// than every one: a device away for a week missed one 6:30 alarm as far as anybody cares, not seven.
func latestIn(s source, from, to time.Time) (time.Time, bool) {
	var last time.Time
	found := false
	at := from
	// Bounded, since a daily alarm across a long absence steps a day at a time.
	for range 400 {
		t, ok := s.next(at.Add(time.Nanosecond))
		if !ok || t.After(to) {
			break
		}
		last, found, at = t, true, t
	}
	return last, found
}

// splitAway divides what fell due while the device was away into what is recent enough to ring now
// and what was missed.
func splitAway(sources []source, since, now time.Time) (resume []source, missed []fellDue) {
	for _, s := range sources {
		at, ok := latestIn(s, since, now)
		if !ok {
			continue
		}
		if now.Sub(at) <= ResumeWithin {
			resume = append(resume, s)
			continue
		}
		missed = append(missed, fellDue{s, at})
	}
	return resume, missed
}

// overdue is what a forward jump of the clock carried past without ringing: due in (last, now], but
// later than the stale window lets ring. Snoozes are left to pruneSnoozes, which says so itself.
func overdue(sources []source, last, now time.Time) []fellDue {
	var out []fellDue
	for _, s := range sources {
		if isSnooze(s) {
			continue
		}
		if at, ok := latestIn(s, last, now); ok && now.Sub(at) > stale {
			out = append(out, fellDue{s, at})
		}
	}
	return out
}

func isSnooze(s source) bool { return strings.HasPrefix(s.key, "snooze:") }

func kind(s source) string {
	if isSnooze(s) {
		return "snooze"
	}
	return "alarm"
}

// away looks at the time the device was not running, once the clock can be trusted: what fell due
// in the last ResumeWithin rings now, and anything older is written down as missed.
func (a *Alarms) away(now time.Time) {
	since, ok := config.Alive()
	if !ok || !since.Before(now) {
		return
	}
	resume, missed := splitAway(a.sources(now), since, now)
	for _, m := range missed {
		a.missed(m)
	}
	for _, s := range resume {
		slog.Info("ringing what fell due while the device was away", "key", s.key)
		a.fire(s, now)
	}
}

// missed writes down a ring that never sounded, and turns off a one-off alarm the way ringing it
// would have, or it would ring a day late at the same time tomorrow.
func (a *Alarms) missed(m fellDue) {
	ring.Missed(kind(m.s), m.s.label, m.at)
	if !m.s.local || m.s.days != config.DaysOnce {
		return
	}
	c := config.Get().Alarms
	if i := slices.IndexFunc(c.List, func(x config.Alarm) bool { return x.ID == m.s.key }); i >= 0 {
		al := c.List[i]
		al.On = false
		if err := config.Set().Alarms().Put(al); err != nil {
			slog.Warn("turning off a missed one-off alarm failed", "err", err)
		}
	}
}

// keepAlive writes down that the device is running: every aliveEvery, and at once when something
// rang, so a restart straight after an alarm never takes that alarm for missed.
func (a *Alarms) keepAlive(now time.Time, rang bool) {
	if !rang && now.Sub(a.aliveAt) < aliveEvery {
		return
	}
	if err := config.KeepAlive(now); err != nil {
		slog.Warn("recording that the device is running failed", "err", err)
		return
	}
	a.aliveAt = now
}
