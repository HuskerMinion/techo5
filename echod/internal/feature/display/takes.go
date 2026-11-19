//go:build !dot

package display

import (
	"fmt"
	"hash/fnv"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
)

// Takes of a word, asked for on the setup page (feature/takes).
//
// The setup page can ask for a recording but not start one: that is a press at the device, and a Show
// or a Spot has no button for it, so it is Start on a card the screen puts up, the way a browser asking
// to be let in is answered. While the recording runs the card stays up with Stop on it, over whatever
// was on the screen and lit at night too: the microphones going somewhere is not something only the
// browser should know.

// asksAtDevice is whether the setup page is asking something of whoever is at the device: a browser to
// be let in, or a recording of takes to be started or stopped. The screen stays up and lit for it.
func asksAtDevice() bool { return setup.Get().Waiting() || takes.Get().Card().Up() }

// pageAsk: the yes of each of the setup page's askings is in the same place (Allow, Start, Stop), so
// a tap answers only the page that was drawn, and only once it has been up askSettle (askLatch). A
// browser that starts asking, or a recording that moves on, under a finger on its way does not take
// the tap meant for what was there.

// pageKey names the asking a frame draws: the browser asking to be let in when there is one, or else
// the takes' card; 0 for none.
func pageKey(c takes.Card) uint64 {
	if c.Up() {
		return takesKey(c)
	}
	if asking := setup.Get().Asking(); asking != "" {
		return letInKey(asking)
	}
	return 0
}

// letInKey names a browser asking to be let in, and takesKey a takes card: a recording's card is
// another page once it has started, so its Stop is not answered by the tap that started it.
func letInKey(asking string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(asking))
	return h.Sum64() | 1<<63
}

func takesKey(c takes.Card) uint64 {
	if !c.Up() {
		return 0
	}
	return (c.ID<<2 | uint64(c.Phase)) &^ (1 << 63)
}

// answerTakes answers the Start card. Start is behind the settings lock, as letting a browser in is:
// somebody at a locked screen must not start a recording a browser asked for. The PIN starts the
// recording that was asked for when Start was tapped, and no other.
func answerTakes(start bool, id uint64) {
	if !start {
		takes.Get().Decline(id)
		return
	}
	if security.Locked() {
		openPIN(tr("record_takes_of_a_word"), func() {
			takes.Get().Begin(id)
			security.Relock()
		})
		return
	}
	takes.Get().Begin(id)
}

// takesWhat is what the card says the takes are of.
func takesWhat(c takes.Card, most int) string {
	w := []rune(c.Word)
	word := c.Word
	if len(w) > most {
		word = string(w[:most-1]) + "…"
	}
	if c.Kind == takes.NotWord {
		return fmt.Sprintf(tr("ui_takes_like_word"), word)
	}
	return fmt.Sprintf(tr("ui_takes_of_word"), word)
}

// Record wake word (feature/takes run.go), in the Voice group of the Sound card: Start, with how many
// takes and how far apart above it, and with two wake words loaded which of them. The tap on Start is the press at the device, so the run starts at
// once; behind the settings lock, as starting the setup page's recording is.

// recordWordRows are the rows: Start, the wake word when there are two to choose from, the count and
// the gap. The word's row is not there with one word loaded: nothing to choose, as before there was a
// choice.
func recordWordRows() []settingRow {
	rec := takes.Get()
	count, gap := rec.RunPlan()
	rows := []settingRow{
		{id: "recordword", label: tr("record_wake_word"), sub: recordWordSub(), kind: ctlButton, button: tr("ui_start")},
	}
	if w, ok := rec.RunWord(); ok && len(rec.RunWords()) > 1 {
		rows = append(rows, settingRow{id: "recordslot", label: tr("wake_word"), kind: ctlStepper, value: w.Name()})
	}
	return append(rows,
		settingRow{id: "recordcount", label: tr("ui_record_takes"), kind: ctlStepper, value: fmt.Sprint(count)},
		settingRow{id: "recordgap", label: tr("ui_record_seconds_between"), kind: ctlStepper, value: fmt.Sprintf("%d s", gap)},
	)
}

// recordWordRefusedFor is how long a start that could not be made is said under Record wake word.
const recordWordRefusedFor = 15 * time.Second

// recordWordSub is what Record wake word says under its name: what has to be set first, a start just
// refused, how the last run went, or where the takes go. Start here needs no Record takes of a word
// switch: the tap at the device is the consent (takes.RunBlocked).
func recordWordSub() string {
	rec := takes.Get()
	switch rec.RunBlocked() {
	case takes.RunNotWakesmith:
		return tr("ui_record_set_wakesmith_first")
	case takes.RunClockNotSet:
		return tr("ui_record_clock_not_set")
	case takes.RunNoWakeWord:
		return tr("ui_record_no_wake_word")
	case takes.RunTakesKept:
		return tr("ui_record_takes_kept_first")
	}
	if r := rec.LastRefusal(); r.Refused && time.Since(r.At) < recordWordRefusedFor {
		return tr("ui_record_busy")
	}
	if last := rec.LastRun(); last.Of > 0 {
		return fmt.Sprintf(tr("ui_record_last_run"), last.Kept, last.Of)
	}
	return tr("ui_record_sends_to_wakesmith")
}

// recordWordTap is a tap on one of the rows: Start, or a step of the wake word, the count (by five) or
// the gap.
func recordWordTap(id string, p part) {
	rec := takes.Get()
	count, gap := rec.RunPlan()
	step := 0
	switch p {
	case partMinus:
		step = -1
	case partPlus:
		step = 1
	}
	switch id {
	case "recordslot":
		rec.StepRunWord(step)
	case "recordcount":
		if step != 0 {
			rec.SetRunPlan(count+5*step, gap)
		}
	case "recordgap":
		if step != 0 {
			rec.SetRunPlan(count, gap+step)
		}
	case "recordword":
		start := func() { _ = takes.Get().StartRun(takes.Get().RunPlan()) }
		if security.Locked() {
			openPIN(tr("record_wake_word"), func() {
				start()
				security.Relock()
			})
			return
		}
		start()
	}
}

// runLine is what a run's card says now: say the word, it wasn't heard, or get ready for the beep.
func runLine(c takes.Card) string {
	switch {
	case c.Listening:
		return tr("ui_say_the_wake_word_now")
	case c.Missed:
		return fmt.Sprintf(tr("ui_didnt_hear_take_again"), c.Take)
	}
	return tr("ui_record_get_ready")
}

// takesTime is how far a recording has come, against its ceiling.
func takesTime(d time.Duration) string {
	s, most := int(d.Seconds()), int(takes.Longest.Seconds())
	s = min(s, most)
	return fmt.Sprintf(tr("ui_takes_time_progress"), s/60, s%60, most/60, most%60)
}
