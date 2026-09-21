//go:build !dot

package display

import (
	"fmt"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// The Sleep timer row: what is playing stops after a while. Nothing rings and nothing is announced —
// a clock radio's sleep button, not one of the device's timers.

var sleepChoices = []time.Duration{
	0, 15 * time.Minute, 30 * time.Minute, 45 * time.Minute, time.Hour, 90 * time.Minute, 2 * time.Hour,
}

func sleepLabels() []string {
	out := make([]string, len(sleepChoices))
	for i, d := range sleepChoices {
		out[i] = sleepLabel(d)
	}
	return out
}

func sleepLabel(d time.Duration) string {
	switch {
	case d == 0:
		return "Off"
	case d == time.Hour:
		return "1 hour"
	case d > time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	case d > time.Hour:
		return fmt.Sprintf("1 hour %d minutes", int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%d minutes", int(d/time.Minute))
}

// sleepIndex is the choice in force: the shortest one still longer than what is left, since the row
// counts down while it runs.
func sleepIndex() int {
	left := media.Get().Sleep().Left()
	if left == 0 {
		return 0
	}
	for i := len(sleepChoices) - 1; i > 0; i-- {
		if sleepChoices[i] <= left {
			return i
		}
	}
	return 1
}

// sleepValue is what the row shows: how long the music has, or Off.
func sleepValue() string {
	left := media.Get().Sleep().Left()
	if left == 0 {
		return "Off"
	}
	if left >= time.Hour {
		return fmt.Sprintf("%d:%02d left", int(left/time.Hour), int(left%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%d min left", int(left/time.Minute)+1)
}

func sleepSub() string {
	if media.Get().Sleep().Left() == 0 {
		return "Stops the music after a while"
	}
	return "The music stops on its own; nothing rings"
}

func chooseSleep(i int) {
	if i < 0 || i >= len(sleepChoices) {
		return
	}
	if sleepChoices[i] == 0 {
		media.Get().Sleep().Cancel()
		return
	}
	media.Get().Sleep().Set(sleepChoices[i])
}

// toneValue is a shelf as the row shows it: plain zero rather than "0 dB", since flat is the tuning
// as it was made and not an adjustment of nothing.
func toneValue(db float64) string {
	if db == 0 {
		return "Flat"
	}
	return fmt.Sprintf("%+.0f dB", db)
}

// toneSub says what the pair of them are for, on the first of the two rows.
func toneSub() string {
	if !config.Get().Speaker.ASPWanted() {
		return "Needs Speaker EQ, which is off"
	}
	return "Adjusts the speaker tuning to the room"
}
