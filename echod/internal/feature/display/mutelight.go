//go:build !dot && !spot && !panel

package display

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// muteLightSaysIt is whether the device's own red mute light already says the microphone is off: an
// Echo Show's is wired to its mute button. The header's "Microphone off" only repeated it, and in a
// bedroom at night it is one more light on the screen (techo5#621). The Skylight has no such light.
// A variable for the tests of the pill itself.
var muteLightSaysIt = func() bool { return !layout.Skylight() }
