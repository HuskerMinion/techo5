package alarm

import (
	"testing"
	"time"
)

// A snooze whose moment went by without ringing is taken off the list.
//
// It used to stay there for good. A one-off already past is inert — next reports nothing for it ever
// again, so due skips it, soonest skips it, and the only two things that ever pruned the list were a
// snooze firing and somebody cancelling them all. Meanwhile the settings sheet went on drawing
// "Snoozed until" and a time that had been and gone: an alarm promised to somebody that was never
// coming.
func TestASnoozeThatPassedIsTakenOffTheList(t *testing.T) {
	now := time.Date(2026, 9, 16, 7, 0, 0, 0, time.UTC)

	snoozed := []source{
		{key: "snooze:wake", label: "Wake up", once: now.Add(-11 * time.Minute)}, // past the stale window
		{key: "snooze:pills", label: "Pills", once: now.Add(-time.Second)},       // only just gone
		{key: "snooze:bread", label: "Bread", once: now.Add(4 * time.Minute)},    // still to come
	}

	live, missed := splitSnoozes(snoozed, now)

	if len(live) != 1 || live[0].key != "snooze:bread" {
		t.Errorf("kept %v, want only the one still to come", keys(live))
	}
	if len(missed) != 2 {
		t.Fatalf("missed %v, want the two that had gone", keys(missed))
	}
	// Reported, not just dropped: the trace is the point of this.
	for _, s := range missed {
		if s.label == "" {
			t.Errorf("a missed snooze has no label to report: %+v", s)
		}
	}
}

// The moment itself has not gone by, so it still counts as live: a snooze due exactly now is rung by
// the pass that runs before this one.
func TestASnoozeDueThisInstantIsNotPruned(t *testing.T) {
	now := time.Date(2026, 9, 16, 7, 0, 0, 0, time.UTC)

	// due() fires on (last, now], so a snooze at exactly now has just rung and removed itself. One
	// still in the list at that moment is one that has not been fired yet, and pruning it here would
	// silence an alarm that was about to sound.
	live, missed := splitSnoozes([]source{{key: "snooze:wake", once: now.Add(time.Nanosecond)}}, now)
	if len(live) != 1 || len(missed) != 0 {
		t.Errorf("a snooze a nanosecond away was pruned: live %v missed %v", keys(live), keys(missed))
	}
}

// A repeating source has no single moment, so it is never a missed snooze.
func TestARepeatingSourceIsNeverPruned(t *testing.T) {
	now := time.Date(2026, 9, 16, 7, 0, 0, 0, time.UTC)

	live, missed := splitSnoozes([]source{{key: "alarm:1", hour: 6, min: 30, days: 0x7f}}, now)
	if len(live) != 1 || len(missed) != 0 {
		t.Errorf("a daily alarm was pruned as a missed snooze: live %v missed %v", keys(live), keys(missed))
	}
}

func keys(ss []source) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.key)
	}
	return out
}
