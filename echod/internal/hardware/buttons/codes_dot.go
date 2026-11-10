//go:build dot

package buttons

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// longHoldAfter is how long the action button is held to start Bluetooth pairing, which is how the
// stock Echo used a long hold too: there is no screen to ask for it on.
const longHoldAfter = 5 * time.Second

// codes are the board's buttons, by evdev key code: the Echo Dot's four, or the Echo Input's two.
var codes = boardCodes(layout.Cupcake())

// boardCodes is the Echo Dot 2's four buttons (the Echo 2nd gen's and the Dot 3's are the same codes),
// or on the Echo Input (cupcake) its two: the action button (gpio-keys, KEY_HELP) and mute
// (gpio-privacy, KEY_MUTE). It has no volume keys, so they are not offered to Home Assistant, where
// they would sit at unknown for good (Missing), and what holds one of them (the reset combination) is
// not looked for (echo-dot-assist, docs/re-cupcake.md).
func boardCodes(cupcake bool) map[uint16]Name {
	if cupcake {
		return map[uint16]Name{
			113: Mute,
			138: Action,
		}
	}
	return map[uint16]Name{
		113: Mute,
		114: VolumeDown,
		115: VolumeUp,
		138: Action,
	}
}

func init() {
	if layout.Cupcake() {
		missing = map[Name]bool{VolumeDown: true, VolumeUp: true}
	}
}

// notButtons are input devices that are not buttons, by name: none here.
var notButtons map[string]bool
