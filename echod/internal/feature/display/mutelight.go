//go:build !dot && !spot && !panel

package display

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// muteLightSaysIt is whether the device's own red mute light already says the microphone is off: an
// Echo Show's is wired to its mute button. There the header's "Microphone off" repeats it, so the
// Show Microphone off switch may take it away (mic_off_pill.go, techo5#621). The Skylight has no such
// light, and always says it. A variable for the tests.
var muteLightSaysIt = func() bool { return !layout.Skylight() }
