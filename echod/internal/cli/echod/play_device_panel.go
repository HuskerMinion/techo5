//go:build panel

package echod

// defaultPlaybackDevice: a panel plays through Android and has no ALSA device of its own; the flag keeps
// the old default for whoever points it at one.
var defaultPlaybackDevice = 23
