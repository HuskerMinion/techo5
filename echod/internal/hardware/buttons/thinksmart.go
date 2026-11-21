//go:build !dot && !spot && !panel

package buttons

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The Lenovo ThinkSmart View's microphone mute is a slide switch, not a button: gpio-keys reports it
// as SW_MICROPHONE_INSERT (4). Each move is reported as a tap of the mute button, which is what makes
// the mute feature read the line again; the switch itself has already cut the microphones.
const swMicrophoneInsert = 0x04

func init() {
	if layout.ThinkSmart() {
		switchTaps = map[uint16]Name{swMicrophoneInsert: Mute}
	}
}
