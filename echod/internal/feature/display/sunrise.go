//go:build !dot

package display

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
)

// Waking to light. For the quarter hour (or whatever is set) before an alarm, the screen comes up
// from almost nothing to the brightness it is set to, so the room is lit before the sound starts.
//
// It lights a panel that the night put out, the way a ring or a call does, and holds it lit while it
// runs. Nothing about the alarm itself changes: this is the light before it, and the alarm rings as
// it always did.

// The curve and the window are the alarm feature's, so that a device with a ring and no screen lights
// the room the same way (feature/alarm/sunrise.go).

var sunriseProgress = func(now time.Time) float64 { return alarm.Get().SunriseProgress(now) }

// sunriseHold is what holds the light at full after an alarm, if anything (alarm.Alarms.WakeHold).
var sunriseHold = func(now time.Time) (until time.Time, snooze, ok bool) { return alarm.Get().WakeHold(now) }

// sunriseGlow is the light held at full after a light-only alarm, if it is (alarm.Alarms.LightOnlyGlow).
var sunriseGlow = func(now time.Time) (until time.Time, ok bool) { return alarm.Get().LightOnlyGlow(now) }

// releaseLight lets go of the wake light held after an alarm, as a stop with nothing sounding does
// (ring.Release).
var releaseLight = ring.Release

func sunriseLevel(progress float64) float64 { return alarm.SunriseLevel(progress) }

// tapLetsGoOfLight is a tap on the wake light's page with nothing ringing. While the light is held at
// full after an alarm, the tap lets it go, as the stop word or Stop in Home Assistant does (#161); it
// was the one thing on the device that could not turn it off. While the light is only coming up before
// an alarm, it does nothing. It reports whether it let the light go.
func tapLetsGoOfLight(now time.Time) bool {
	if _, _, ok := sunriseHold(now); !ok || !releaseLight() {
		return false
	}
	slog.Info("screen: a tap let go of the held wake light")
	return true
}

// The Wake with light row's choices.
var sunriseChoices = []int{0, 5, 10, 15, 20, 30}

func sunriseLabels() []string {
	out := make([]string, len(sunriseChoices))
	for i, m := range sunriseChoices {
		out[i] = tr("ui_off")
		if m > 0 {
			out[i] = fmt.Sprintf(tr("minutes_before"), m)
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
		return fmt.Sprintf(tr("ui_d_min"), m)
	}
	return tr("ui_off")
}

func sunriseSub() string {
	now := time.Now()
	// Held at full after an alarm, it says what for (#161), in few enough words for the Spot's card.
	if until, snooze, ok := sunriseHold(now); ok {
		switch {
		case until.IsZero():
			return tr("sunrise_ringing")
		case snooze:
			return fmt.Sprintf(tr("sunrise_snooze"), clockText(until))
		}
		if glow, held := sunriseGlow(now); held && glow.Equal(until) {
			return fmt.Sprintf(tr("sunrise_until"), clockText(until))
		}
		return fmt.Sprintf(tr("sunrise_alarm"), clockText(until))
	}
	if sunriseProgress(now) > 0 {
		return tr("sunrise_rising")
	}
	return tr("sunrise_default_description")
}

// anySunrise is whether any alarm will light the room, for whether the sun's face is worth a row.
func anySunrise() bool {
	c := config.Get().Alarms
	for _, a := range c.List {
		if c.SunriseFor(a) > 0 {
			return true
		}
	}
	return c.SunriseMinutes > 0
}

// alarmSunrise is the alarm editor's Wake with light: the default first, then none, then minutes.
func alarmSunrise() (labels []string, values []int) {
	def := tr("default_off")
	if m := config.Get().Alarms.SunriseMinutes; m > 0 {
		def = fmt.Sprintf(tr("ui_d_min"), m)
	}
	labels, values = []string{fmt.Sprintf(tr("same_as_default"), def), tr("ui_off")}, []int{0, config.SunriseOff}
	for _, m := range sunriseChoices[1:] {
		labels, values = append(labels, fmt.Sprintf(tr("minutes_before"), m)), append(values, m)
	}
	return labels, values
}

// alarmSunriseIndex is which of alarmSunrise an alarm has.
func alarmSunriseIndex(a config.Alarm) int {
	_, values := alarmSunrise()
	for i, v := range values {
		if v == a.Sunrise {
			return i
		}
	}
	return 0
}

// alarmSunriseValue is what the editor's row says for an alarm.
func alarmSunriseValue(a config.Alarm) string {
	// A light-only alarm always has a light (config.Alarms.SunriseFor), so where its own says Off, or
	// Default with no default, the row says the minutes it gets instead.
	if c := config.Get().Alarms; a.LightOnly && !a.Remind && !a.Silent && (a.Sunrise < 0 || a.Sunrise == 0 && c.SunriseMinutes <= 0) {
		return fmt.Sprintf(tr("ui_d_min"), c.SunriseFor(a))
	}
	switch {
	case a.Sunrise < 0:
		return tr("ui_off")
	case a.Sunrise > 0:
		return fmt.Sprintf(tr("ui_d_min"), a.Sunrise)
	}
	return tr("default_option")
}

func chooseSunrise(i int) {
	if i < 0 || i >= len(sunriseChoices) {
		return
	}
	if err := config.Set().Alarms().SunriseMinutes(sunriseChoices[i]); err != nil {
		slog.Warn("saving the wake light failed", "err", err)
	}
}
