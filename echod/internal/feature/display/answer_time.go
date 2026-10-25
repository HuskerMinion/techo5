//go:build !dot

package display

import (
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// How long a voice turn's words stay on the screen once it is over: five seconds unless somebody chose
// otherwise, up to staying until they are tapped away. A tap clears them at any length.

// answerTimes are the choices, in the order the screen and Home Assistant offer them. Seconds 0 is the
// default of five; -1 is until tapped.
var answerTimes = []struct {
	label   string
	seconds int
}{
	{"5 seconds", 0},
	{"10 seconds", 10},
	{"20 seconds", 20},
	{"Until tapped", -1},
}

// answerTimeOptions are the choices' labels.
func answerTimeOptions() []string {
	out := make([]string, len(answerTimes))
	for i, t := range answerTimes {
		out[i] = t.label
	}
	return out
}

// answerTimeIndex is the saved choice's place in answerTimes; a value no choice has reads as the default.
func answerTimeIndex() int {
	v := config.Get().Screen.AnswerSeconds
	for i, t := range answerTimes {
		if t.seconds == v {
			return i
		}
	}
	return 0
}

// linger is how long the last turn's words stay on the screen after it ends.
func linger() time.Duration {
	switch v := answerTimes[answerTimeIndex()].seconds; {
	case v < 0:
		return untilTapped
	case v == 0:
		return 5 * time.Second
	default:
		return time.Duration(v) * time.Second
	}
}

// setAnswerTime saves the choice at i and shows it in Home Assistant.
func setAnswerTime(s *esphome.Select, i int) {
	if i < 0 || i >= len(answerTimes) {
		return
	}
	if err := config.Set().Screen().AnswerSeconds(answerTimes[i].seconds); err != nil {
		slog.Error("saving the answer time failed", "err", err)
		return
	}
	s.Set(answerTimes[i].label)
}

// answerTimeSelect is the Home Assistant setting.
func answerTimeSelect() *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_answer_time",
			Name:     "Answer time on screen",
			Icon:     "mdi:message-text-clock-outline",
			Category: esphome.CategoryConfig,
		},
		Options: answerTimeOptions(),
	}
	s.OnCommand = func(v string) {
		for i, t := range answerTimes {
			if t.label == v {
				setAnswerTime(s, i)
				return
			}
		}
	}
	return s
}

// answerUp is whether a finished turn's words are on the screen now, as a page of their own. Not under
// a cue: there the page is still up and keeps its gestures, with the words as captions over it, which
// a tap on them puts away (captionsTap), or with no words at all.
func (d *Display) answerUp(now time.Time) bool {
	if cueOn() {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.view.Phase == "idle" && (d.view.Heard != "" || d.view.Reply != "") && !d.quiet &&
		now.Sub(d.viewAt) < linger()
}

// clearAnswer puts a finished turn's words away, as a screen command does: the clock comes back, and the
// next turn shows its own words again.
func (d *Display) clearAnswer() {
	d.mu.Lock()
	d.quiet = true
	d.mu.Unlock()
	slog.Info("screen: answer cleared by touch")
	d.wake()
}
