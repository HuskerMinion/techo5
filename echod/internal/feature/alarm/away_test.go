package alarm

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Away across a week, a daily alarm was missed once as far as anybody cares: the latest time.
func TestLatestInIsTheLastTimeInTheWindow(t *testing.T) {
	daily := source{hour: 6, min: 30, days: config.DaysEvery}
	got, ok := latestIn(daily, at(14, 12, 0), at(21, 9, 0))
	if !ok || !got.Equal(at(21, 6, 30)) {
		t.Errorf("latest = %v %v, want %v", got, ok, at(21, 6, 30))
	}
	if _, ok := latestIn(daily, at(21, 7, 0), at(21, 9, 0)); ok {
		t.Error("found a time in a window the alarm was not due in")
	}
	snooze := source{key: "snooze:a", once: at(16, 7, 9)}
	if got, ok := latestIn(snooze, at(16, 7, 0), at(16, 8, 0)); !ok || !got.Equal(at(16, 7, 9)) {
		t.Errorf("snooze latest = %v %v", got, ok)
	}
}

// Just now rings, since a restart landing on an alarm must not lose it; longer ago is missed, since a
// device that crashes while ringing must not ring on every start.
func TestAwayRingsTheRecentAndWritesDownTheRest(t *testing.T) {
	now := at(16, 6, 31)
	recent := source{key: "recent", hour: 6, min: 30, days: config.DaysEvery}
	old := source{key: "old", hour: 5, min: 0, days: config.DaysEvery}
	later := source{key: "later", hour: 7, min: 0, days: config.DaysEvery}

	resume, missed := splitAway([]source{recent, old, later}, at(16, 4, 0), now)
	if len(resume) != 1 || resume[0].key != "recent" {
		t.Errorf("resumed %v, want only the one due a minute ago", resume)
	}
	if len(missed) != 1 || missed[0].s.key != "old" || !missed[0].at.Equal(at(16, 5, 0)) {
		t.Errorf("missed %v, want the 5:00 one", missed)
	}
}

// A clock jumping forward carries past an alarm: that is missed, and says so. A snooze is left to
// pruneSnoozes, which says so itself, so it is not said twice.
func TestAClockJumpLeavesATrace(t *testing.T) {
	alarm := source{key: "a", hour: 6, min: 30, days: config.DaysEvery}
	snooze := source{key: "snooze:a", once: at(16, 6, 40)}
	near := source{key: "near", hour: 7, min: 55, days: config.DaysEvery}

	got := overdue([]source{alarm, snooze, near}, at(16, 6, 0), time.Time{}, at(16, 8, 0))
	if len(got) != 1 || got[0].s.key != "a" {
		t.Errorf("overdue = %v, want the 6:30 alarm alone", got)
	}

	// A clock that came up behind the last record, then jumped: the 6:30 alarm rang before the
	// record at 7:00, so it is not missed.
	if got := overdue([]source{alarm}, at(16, 6, 0), at(16, 7, 0), at(16, 8, 0)); len(got) != 0 {
		t.Errorf("overdue from before the record = %v, want nothing", got)
	}
}
