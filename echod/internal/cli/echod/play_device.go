//go:build !panel

package echod

import "github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"

// defaultPlaybackDevice is the board's own playback device (hardware/speaker): 23 on the Dot 2, DL2
// Playback (1) on the Dot 5.
var defaultPlaybackDevice = speaker.PlaybackDevice
