//go:build !dot

package display

import (
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The Quiet hours row, beside the sleep timer on Sound & Voice.
//
// Quiet hours were in the config and in the code that reads them long before there was any way to
// set them: they could be typed into state.json by hand, which is why nobody used them. What they
// silence is the noise the device makes about itself — the wake tone, the tones for a failed or
// dropped turn, an announcement arriving from another device. What they never silence is anything
// somebody asked for, and never anything on the screen or the ring.

// quietPresets are the windows offered, empty first for never. The same shape and the same hours as
// the screen's night, since a house that dims the screen at ten usually wants the noises to stop at
// ten as well.
var quietPresets = []string{"", "22-7", "22-6", "23-7", "0-7", "21-7", "23-8"}

// quietValue is the row's reading.
func quietValue() string { return nightText(config.Get().Speaker.QuietHours) }

// quietSub says what the setting covers, because "quiet" on its own would have somebody expecting a
// silent alarm clock.
func quietSub() string {
	if config.Get().Speaker.QuietHours == "" {
		return "Alarms and timers always sound"
	}
	return "The device's own tones only; alarms still sound"
}

// quietIndex is which preset is in force, for the picker's tick.
func quietIndex() int {
	cur := config.Get().Speaker.QuietHours
	for i, p := range quietPresets {
		if p == cur {
			return i
		}
	}
	return -1
}

// quietLabels are those presets as the picker shows them.
func quietLabels() []string {
	out := make([]string, len(quietPresets))
	for i, p := range quietPresets {
		out[i] = nightText(p)
	}
	return out
}

// chooseQuiet puts the i'th preset in force.
func chooseQuiet(i int) {
	if i < 0 || i >= len(quietPresets) {
		return
	}
	if err := config.Set().Speaker().QuietHours(quietPresets[i]); err != nil {
		slog.Error("saving a setting failed", "setting", "quiet_hours", "err", err)
	}
}
