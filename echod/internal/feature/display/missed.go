//go:build !dot

package display

import (
	"fmt"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// missedNote is the line that says a ring fell due and never sounded, or nothing. The latest one by
// name and time, and how many more, so a night away is one line rather than a list.
//
// short is the round face, where a line wider than the circle is cut off by its edge: there it is the
// ring and the time and nothing else.
func missedNote(now time.Time, short bool) string {
	list := ring.Missing()
	if len(list) == 0 {
		return ""
	}
	m := list[len(list)-1]
	if r := []rune(m.Label); short && len(r) > 12 {
		m.Label = string(r[:11]) + "…"
	}
	line := fmt.Sprintf(tr("ui_missed_s_at_s"), missedWhat(m), clockText(m.Due))
	if short {
		return line
	}
	if ago := daysAgo(m.Due, now); ago != "" {
		line += " " + ago
	}
	if n := len(list) - 1; n > 0 {
		line += " · " + fmt.Sprintf(tr("ui_and_n_more"), n)
	}
	return line
}

// missedWhat is what was missed, in the screen's language, with its label.
func missedWhat(m ring.MissedRing) string {
	what := m.What
	switch m.What {
	case "alarm":
		what = tr("alarm")
	case "timer":
		what = tr("timer")
	case "snooze":
		what = tr("ui_snooze_ring")
	}
	if m.Label != "" {
		return fmt.Sprintf("%s %q", what, m.Label)
	}
	return what
}

// daysAgo says which day a past time was on, when it was not today.
func daysAgo(t, now time.Time) string {
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.In(now.Location()).Date()
	days := int(time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC).Sub(time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)).Hours() / 24)
	switch {
	case days <= 0:
		return ""
	case days == 1:
		return tr("ui_missed_yesterday")
	case days < 7:
		return fmt.Sprintf(tr("ui_missed_on_s"), locale.Weekday(t, screenLang()))
	}
	return fmt.Sprintf(tr("ui_missed_on_s"), locale.MonthDay(t, screenLang()))
}

// onMissed calls f when what was missed changes. Here rather than in display.go, whose frame loop
// has a local called ring.
func onMissed(f func()) { ring.MissedChanged.Listen(func(struct{}) { f() }) }
