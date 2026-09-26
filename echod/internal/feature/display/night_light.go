//go:build !dot

package display

import "github.com/HuskerMinion/techo5/echod/internal/config"

// atNightOptions is what the night does to a Show's screen: puts it out, leaves a night light, or
// leaves only the clock, in dim red.
var atNightOptions = []string{"Screen off", "Night light", "Red clock"}

func atNightIndex() int {
	switch sc := config.Get().Screen; {
	case sc.NightLight && sc.NightRed:
		return 2
	case sc.NightLight:
		return 1
	}
	return 0
}

// The red clock's styles, as they are stored (render_night.go draws them).
const (
	nightStylePlain = ""
	nightStyleLED   = "led"
	nightStyleFlip  = "flip"
)

// nightStyleOptions are the red clock's looks, as the screen and Home Assistant name them, in the order
// of nightStyles.
var (
	nightStyleOptions = []string{"Plain", "LED", "Flip"}
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
	return pickerView{title: "Clock style", opts: nightStyleOptions, cur: nightStyleIndex()}, true
}
