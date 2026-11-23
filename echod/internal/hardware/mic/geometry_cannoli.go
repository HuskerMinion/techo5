//go:build dot && cannoli

package mic

import "github.com/HuskerMinion/techo5/echod/internal/lib/array"

// arrayGeometry is where the Echo Dot 5th gen's three microphones are, in metres in the plane of the top
// seen from above, x toward LED segment 0 (the back, over the power socket) and bearings clockwise with
// the segments (lib/array).
//
// The shape is Amazon's: codebook_Cancheese.txt in /vendor/etc/audio-algorithms gives an L of 22.5 mm
// legs. Where it lies, and which channel is which corner, was measured on one unit (2026-11-23,
// cmd/beamcal): white noise from a phone a metre out toward each of the twelve LEDs, recorded at 96 kHz.
// The free fit came out as 21.6, 20.5 and 32.4 mm with ch1 in the corner; this is the L laid on it
// (1.2 mm rms from the fit). With it, all twelve takes localize within 19 degrees, nine within 10. The
// take from straight behind came out at 163 degrees twice, until it was taken again with the phone
// where the LED is: 4 degrees off, and refitting with it moved no microphone by more than 0.2 mm.
var arrayGeometry = array.Geometry{
	{X: 0.00203, Y: 0.01665},
	{X: -0.01039, Y: -0.00211},
	{X: 0.00836, Y: -0.01454},
}
