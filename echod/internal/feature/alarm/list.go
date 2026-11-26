package alarm

import (
	"fmt"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// Listing is what alarms_list answers Home Assistant with: everything on the device that will or
// might ring, with the ids the delete and cancel actions take.
type Listing struct {
	Ringing  *ListedRing    `json:"ringing"`
	Alarms   []ListedAlarm  `json:"alarms"`
	Snoozed  []ListedSnooze `json:"snoozed"`
	Followed []ListedHelper `json:"followed"`
	Timers   []ListedTimer  `json:"timers"`
}

type ListedRing struct {
	Label string `json:"label"`
	Since string `json:"since"`
	// Kind is "alarm" or "timer", so an automation can tell a ringing alarm from a ringing timer (#736).
	Kind string `json:"kind"`
}

type ListedAlarm struct {
	ID    string `json:"id"`
	Time  string `json:"time"` // 24-hour "07:30"
	Days  string `json:"days"`
	Date  string `json:"date,omitempty"` // 2026-09-29 for a one-off on a particular day
	Label string `json:"label"`
	On    bool   `json:"on"`
	Next  string `json:"next"` // RFC 3339, empty when it is off or will not ring again

	// Reminder is one said once rather than rung, and RingOn the other devices it goes to.
	Reminder bool     `json:"reminder"`
	RingOn   []string `json:"ring_on"`

	// Silent goes off as an event only, and LightOnly as the wake light only (#164).
	Silent    bool `json:"silent"`
	LightOnly bool `json:"light_only"`

	// WakeWith is what it wakes with (#651), as alarm_wake_with takes it: "alarm_sound", "radio: <name>"
	// or "music_assistant: <name>".
	WakeWith string `json:"wake_with"`
}

type ListedSnooze struct {
	Label string `json:"label"`
	At    string `json:"at"`
}

type ListedHelper struct {
	Entity string `json:"entity"`
	Label  string `json:"label"`
	Armed  bool   `json:"armed"`
	Next   string `json:"next"`
}

type ListedTimer struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Left    int    `json:"left_seconds"`
	Total   int    `json:"total_seconds"`
	Running bool   `json:"running"`
	// Local is a timer of the device's own, which timer_cancel can cancel. The others are Home
	// Assistant's, canceled where they were set.
	Local bool `json:"local"`
	// Silent is one started with timer_start_silent, which only shows and does not ring: the wait of an
	// automation, which cancels it itself.
	Silent bool `json:"silent"`
}

// listing builds the answer from what the device holds; kept apart from the Alarms so it can be tested
// without one. rung is the timer ringing, nil when none is.
func listing(now time.Time, v View, timers []timer.Countdown, rung *timer.Rung) Listing {
	l := Listing{Alarms: []ListedAlarm{}, Snoozed: []ListedSnooze{}, Followed: []ListedHelper{}, Timers: []ListedTimer{}}
	switch {
	case v.Ringing != nil:
		l.Ringing = &ListedRing{Label: v.Ringing.Label, Since: stamp(v.Ringing.At), Kind: "alarm"}
	case rung != nil:
		// A ringing timer has left the timers, and rings from the timers' own ring rather than the
		// alarms', so without this nothing in the answer would say it rings (#736).
		l.Ringing = &ListedRing{Label: rung.Name, Since: stamp(rung.Since), Kind: "timer"}
	}
	for _, al := range v.Local {
		next := ""
		if al.On {
			src := source{hour: al.Hour, min: al.Minute, days: al.Days, skips: skipMoments(v.Skipped, al.ID)}
			if at, ok := al.OnDate(); ok {
				src.once = at
			}
			if at, ok := src.next(now); ok {
				next = stamp(at)
			}
		}
		l.Alarms = append(l.Alarms, ListedAlarm{
			ID: al.ID, Time: fmt.Sprintf("%02d:%02d", al.Hour, al.Minute), Days: al.When(), Date: al.Date,
			Label: al.Label, On: al.On, Next: next, Reminder: al.Remind, RingOn: append([]string{}, al.RingOn...),
			Silent: al.Silent && !al.Remind, LightOnly: al.LightOnly && !al.Remind && !al.Silent,
			WakeWith: al.Waking().String(),
		})
	}
	for _, s := range v.Snoozed {
		l.Snoozed = append(l.Snoozed, ListedSnooze{Label: s.Label, At: stamp(s.At)})
	}
	for _, f := range v.Followed {
		l.Followed = append(l.Followed, ListedHelper{Entity: f.Entity, Label: f.Label, Armed: f.Armed, Next: stamp(f.At)})
	}
	for _, c := range timers {
		l.Timers = append(l.Timers, ListedTimer{
			ID: c.ID, Label: c.Name, Left: int(c.Left.Round(time.Second) / time.Second),
			Total: int(c.Total.Round(time.Second) / time.Second), Running: c.Active, Local: c.Local, Silent: c.Silent,
		})
	}
	return l
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
