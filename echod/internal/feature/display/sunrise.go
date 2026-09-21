//go:build !dot

package display

import (
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
)

// Waking to light. For the quarter hour (or whatever is set) before an alarm, the screen comes up
// from almost nothing to the brightness it is set to, so the room is lit before the sound starts.
//
// It lights a panel that the night put out, the way a ring or a call does, and holds it lit while it
// runs. Nothing about the alarm itself changes: this is the light before it, and the alarm rings as
// it always did.

// sunriseFloor is where the ramp starts, as a fraction of the screen's brightness: enough to see the
// clock in a dark room and not enough to wake anybody by itself.
const sunriseFloor = 0.02

// sunriseProgress is how far into the light we are: 0 outside it, rising to 1 as the alarm's time
// arrives. A snoozed alarm does not light the room again — it was already light the first time.
func sunriseProgress(now time.Time) float64 {
	mins := config.Get().Alarms.SunriseMinutes
	if mins <= 0 {
		return 0
	}
	next := alarm.Get().View(now).Next
	if next == nil || next.Snoozed {
		return 0
	}
	window := time.Duration(mins) * time.Minute
	left := next.At.Sub(now)
	if left <= 0 || left > window {
		return 0
	}
	return 1 - float64(left)/float64(window)
}

// sunriseLevel is the fraction of the screen's brightness the ramp asks for, eased so that most of
// the change happens near the end rather than the moment it starts.
func sunriseLevel(progress float64) float64 {
	if progress <= 0 {
		return 0
	}
	return sunriseFloor + (1-sunriseFloor)*math.Pow(math.Min(progress, 1), 2)
}

// The Wake with light row's choices.
var sunriseChoices = []int{0, 5, 10, 15, 20, 30}

func sunriseLabels() []string {
	out := make([]string, len(sunriseChoices))
	for i, m := range sunriseChoices {
		out[i] = "Off"
		if m > 0 {
			out[i] = fmt.Sprintf("%d minutes before", m)
		}
	}
	return out
}

func sunriseIndex() int {
	cur := config.Get().Alarms.SunriseMinutes
	for i, m := range sunriseChoices {
		if m == cur {
			return i
		}
	}
	return 0
}

// sunriseValue is what the row shows, counting down while the light is coming up.
func sunriseValue() string {
	if p := sunriseProgress(time.Now()); p > 0 {
		return fmt.Sprintf("%d%%", int(sunriseLevel(p)*100))
	}
	if m := config.Get().Alarms.SunriseMinutes; m > 0 {
		return fmt.Sprintf("%d min", m)
	}
	return "Off"
}

func sunriseSub() string {
	if sunriseProgress(time.Now()) > 0 {
		return "The light is coming up now"
	}
	return "The screen lights the room before an alarm rings"
}

func chooseSunrise(i int) {
	if i < 0 || i >= len(sunriseChoices) {
		return
	}
	if err := config.Set().Alarms().SunriseMinutes(sunriseChoices[i]); err != nil {
		slog.Warn("saving the wake light failed", "err", err)
	}
}
