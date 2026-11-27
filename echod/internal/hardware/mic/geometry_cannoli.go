//go:build dot && cannoli

package mic

import "github.com/HuskerMinion/techo5/echod/internal/lib/array"

// arrayGeometry is where the Echo Dot 5th gen's three microphones are, in meters in the plane of the top
// seen from above, x toward LED segment 0 (the back, over the power socket) and bearings clockwise with
// the segments (lib/array).
//
// The shape is Amazon's: codebook_Cancheese.txt in /vendor/etc/audio-algorithms gives an L of 22.5 mm
// legs. Where it lies, and which channel is which corner, was measured on one unit (2026-11-23,
// cmd/beamcal): white noise from a phone a meter out toward each of the twelve LEDs, recorded at 96 kHz,
// in two rounds of twelve. The free fit over both came out as 21.1, 21.5 and 31.1 mm with ch1 in the
// corner; this is the L laid on it (0.7 mm rms from the fit, 0.3 mm from the first round's alone).
// Through the daemon's finder all 24 takes localize within 20 degrees, 7.7 on average. That scatter is
// the phone's placement and the room: the same LED in the two rounds differs by up to 22 degrees, and
// the geometry of either round alone does as well on both.
var arrayGeometry = array.Geometry{
	{X: 0.00172, Y: 0.01668},
	{X: -0.01035, Y: -0.00230},
	{X: 0.00863, Y: -0.01438},
}
