//go:build !dot && !spot && !panel

package speaker

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The Lenovo ThinkSmart View (APQ8053 on the msm8953-mainline kernel) plays through the Qualcomm
// audio DSP: the card "cd-18781y" has MultiMedia1 as its playback front end (hw:0,0), which reaches
// the TAS5782M amplifier on the quaternary MI2S only through a mixer route, off until it is set. With
// the route off the front end has no back end and does not open, so it is the openSequence too.
//
// The amplifier's "Speaker Amp Master Playback Volume" runs 0 (mute) to 85 (0 dB), a dB a step. It is
// set to its 0 dB top and the volume curve is applied in software, as on every Show. The card takes
// S16_LE and S24_LE, and plays S24_LE as silence, so it is sent 16 bits, which is what Bits is.
//
// The DSP's front end (q6asm-dai) takes periods only in multiples of 480 frames, 10 ms at the card's
// 48 kHz, so the ring's period is the player's 768 rounded up to 960 (periodStep).
//
// There is no vendor partition and no driver tuning to read, and no loopback of what plays: the echo
// reference is the software one (softRef).
var thinkSmartRoutes = []kctl{
	{name: "QUAT_MI2S_RX Audio Mixer MultiMedia1", level: 1},
}

var thinkSmartInit = append(thinkSmartRoutes, kctl{name: "Speaker Amp Master Playback Volume", level: 85})

// onThinkSmart picks the ThinkSmart View's value over any other.
func onThinkSmart[T any](thinkSmart, other T) T {
	if layout.ThinkSmart() {
		return thinkSmart
	}
	return other
}

func init() {
	if layout.ThinkSmart() {
		DriverTuning = false
		periodStep = 480
	}
}
