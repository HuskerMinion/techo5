//go:build !dot && (spot || panel)

package speaker

// gainOnPath is off: the Speaker gain setting on this build is the TAS5805M's, where a board has one
// (gain.go). On the Dot it is the codec's driver gain (paths_dot.go), and on athena's mainline kernel
// the amplifier's digital volume (paths_cronos.go).
const gainOnPath = false

// withSpeakerGain is seq as it is: no path here carries the Speaker gain.
func withSpeakerGain(seq []kctl, _ Output, _ int) []kctl { return seq }

// speakerGainControls is nothing: no control here carries the Speaker gain with the path.
func speakerGainControls(int) []kctl { return nil }
