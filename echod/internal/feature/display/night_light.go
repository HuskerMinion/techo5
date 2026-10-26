//go:build !dot

package display

import (
	"image/color"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// atNightChoice is one of the Screen at night choices: its name, and the settings it is.
type atNightChoice struct {
	label      string
	light, red bool // config Screen.NightLight and Screen.NightRed
	dark       bool // config Screen.NightDark: the Spot's Screen off
}

// atNightOptions are atNightChoices' names, as the screen and Home Assistant list them. The choices are
// each device's own: the Show's go dark, leave a night light or leave only the clock; the Spot's dim,
// leave only the clock or go dark.
var atNightOptions = func() []string {
	out := make([]string, len(atNightChoices))
	for i, c := range atNightChoices {
		out[i] = c.label
	}
	return out
}()

func atNightIndex() int {
	sc := config.Get().Screen
	for i, c := range atNightChoices {
		if c.light == sc.NightLight && c.red == (sc.NightLight && sc.NightRed) && c.dark == (!sc.NightLight && sc.NightDark) {
			return i
		}
	}
	return 0
}

// nightClockOn is whether the night is the red clock alone.
func nightClockOn() bool {
	sc := config.Get().Screen
	return sc.NightLight && sc.NightRed
}

var (
	// nightRed is the night clock's lit red: dim, so with the backlight at the night light's level it
	// reads across a dark room without lighting it.
	nightRed = color.RGBA{150, 14, 0, 255}
	// nightGhost is an LED segment that is not lit, faint, as on a real LED clock.
	nightGhost = color.RGBA{26, 3, 0, 255}
	// The flip clock is not red: charcoal cards, a shade lighter above the split than below it, the
	// hinges at their sides, and dim off-white digits.
	flipTop   = color.RGBA{36, 36, 35, 255}
	flipLower = color.RGBA{27, 27, 26, 255}
	flipHinge = color.RGBA{52, 52, 50, 255}
	flipInk   = color.RGBA{158, 152, 140, 255}
	flipSplit = color.RGBA{0, 0, 0, 255}
)

// The night clock's styles, as they are stored (render_night.go draws them).
const (
	nightStylePlain = ""
	nightStyleLED   = "led"
	nightStyleFlip  = "flip"
)

// nightStyleOptions are the night clock's looks, as the screen and Home Assistant name them, in the order
// of nightStyles.
var (
	nightStyleOptions = []string{"Red", "Red LED", "Flip"}
	nightStyles       = []string{nightStylePlain, nightStyleLED, nightStyleFlip}
)

func nightStyleIndex() int {
	v := config.Get().Screen.NightClockStyle
	for i, s := range nightStyles {
		if s == v {
			return i
		}
	}
	return 0
}

func nightStyleLabel() string { return nightStyleOptions[nightStyleIndex()] }

func nightStylePicker() (pickerView, bool) {
	return pickerView{title: tr("night_clock"), opts: nightStyleOptions, cur: nightStyleIndex()}, true
}
