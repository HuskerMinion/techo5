//go:build !dot && !spot && !panel

package mic

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The Lenovo ThinkSmart View has two digital microphones on the codec's decimators, recorded through
// the Qualcomm audio DSP's MultiMedia2 front end (hw:0,1), two channels of S16_LE. The routes are the
// ones postmarketOS's UCM profile for the card sets (Lenovo/cd-18781y/HiFi.conf): the tertiary MI2S
// into MultiMedia2, DMIC1 and DMIC2 on the two decimators and their CIC filters, and the decimators'
// digital volume at 115 (+31 dB), read off a unit 2026-11-19. The enumerated ones are written by
// item: DEC1 MUX 4 is DMIC1, DEC2 MUX 5 is DMIC2, CIC MUX 1 is DMIC.
//
// The front end takes periods only in multiples of 480 frames, which the capture's 10 ms periods are
// at 48 kHz and not at 16, so the microphones are read at 48 kHz and brought down to 16.
//
// The card has no loopback of what plays, so the echo reference is the software one, made from what
// the speaker sent (lib/echoref), as on athena's mainline kernel before its card had a reference.
var thinkSmartKernel = athenaKernel{
	micDevice:   1,
	micChannels: 2,
	micBits:     16,
	micFull:     1 << 15,
	refDevice:   -1,
	controls: []athenaControl{
		{"MultiMedia2 Mixer TERT_MI2S_TX", 1},
		{"DEC1 MUX", 4},
		{"DEC2 MUX", 5},
		{"CIC1 MUX", 1},
		{"CIC2 MUX", 1},
		{"TX1 Digital Volume", 115},
		{"TX2 Digital Volume", 115},
	},
	softRef:    true,
	micRates:   []int{48000},
	micPeriods: 8,
}

func init() {
	if layout.ThinkSmart() {
		boardKernel = &thinkSmartKernel
	}
}
