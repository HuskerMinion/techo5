//go:build !dot

package display

import (
	"fmt"
	"log/slog"
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

// The curve and the window are the alarm feature's, so that a device with a ring and no screen lights
// the room the same way (feature/alarm/sunrise.go).

func sunriseProgress(now time.Time) float64 { return alarm.Get().SunriseProgress(now) }

func sunriseLevel(progress float64) float64 { return alarm.SunriseLevel(progress) }

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
