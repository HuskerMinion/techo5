//go:build dot && cannoli

package mic

import (
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

// The Echo Dot 5th gen (cannoli) capture path: the converter's own stream on the speaker's card, without
// the HiFi5 DSP. The microphones go into a TI TLV320ADC5140 (i2c 4-004c), which the MT8519's AFE reads on
// eTDM1 (four slots at 96 kHz, 32 bits: the DVT device tree's etdm1-in-fix-*). ETDM1 Capture lands on the
// AFE's inputs I42..I45 (mt8519-afe-pcm.c in the 6.5.5.5 GPL tree), and the interconnect takes them to
// O26..O29, the first four channels of the UL9 memory interface: mt-snd-card's capture device 9,
// "UL9 Capture" (listed on the unit, 2026-11-21). Fire OS hands eTDM1 to the DSP instead (Amazon's
// voice_dsp_card and its closed dsp_lifecycle make a card of their own, sndvoicedspcard), which a slot-B
// boot does not load; the DSP shares UL9 there.
//
// So the capture is athena's paired one (athena.go) on card 0, device 9: the microphones decimated to
// 16 kHz by athena's filters, with the playback in the same stream (cannoliRefControls). The
// converter is the 2nd gen Echo Show 8's, on the same Amazon driver, and is woken the same way: its
// "ADC Reset A" handler takes it out of sleep (athena.go, athenaControls), which nothing else does.
//
// The converter has four inputs and the board three microphones. On one unit (2026-11-21,
// `echod tools mic`, at the default 20 dB): ch0..ch2 are live, a quiet room at -57 to -60 dBFS rms and
// speech at -46 to -51; ch3 stays at -80 whatever is said, the unwired input, so the frame takes the
// first three. The samples are left-aligned in the 32 bits (micFull 1<<31): read against 2^23, as
// athena's on the same converter are, that room would sit 48 dB louder than full scale allows.
// Which channel is which microphone on the top is not known yet.
const (
	Channels      = 5
	CaptureDevice = cannoliMicDevice

	Mics     = 3
	RefFirst = 3
	Refs     = 2

	maxMics = 3

	// CenterMic: no microphone in the middle, so ch2 stands in: of the three it hears the speaker least,
	// 6 dB under ch0 and ch1 (`echod tools echo`, cannoliStreamRef), which is the most room for a voice
	// over the music once the canceller has taken what it can. ch0, as on the Dot 3, was the loudest.
	CenterMic = 2

	// CancelOnMix runs the canceller on the chosen mix, so Microphone mixing decides what the wake word
	// hears over music too: Center mic is ch2 alone, All microphones (and Delay and sum, which falls back
	// to it while canceling) the average of the three. Which hears a voice over music better is not
	// measured yet; with it on, the two are compared from Home Assistant without a new build.
	CancelOnMix = true

	cannoliMicDevice = 9
	// cannoliMicChannels: UL9 opened six wide, O26..O31: the converter's four slots, then the playback.
	cannoliMicChannels = 6
	cannoliMicRate     = 96000
)

// cannoliStreamRef are UL9's channels 4 and 5 (O30, O31), where cannoliRefControls put DL2's two
// channels (I40, I41): the stream the speaker plays, as the AFE hands it to eTDM2, recorded with the
// microphones on the same memory interface and clock. UL9 takes O26..O41 (mt8519-afe-pcm.c: "UL9" from
// O26 to O41; "O30 I40 Switch" on AFE_CONN30_1 bit 8, "O31 I41 Switch" on AFE_CONN31_1 bit 9). It is
// the digital stream before the MAX98396, so it does not grow with the amplifier's gain, and it holds
// none of the speaker's distortion.
//
// Measured on one unit (2026-11-22, `echod tools echo`, five runs of the sweep and of white
// noise, all alike): the microphones hear the speaker 8 samples at 16 kHz (0.5 ms) after this
// reference, ch2 9 (0.56 ms), so the reference leads and the canceller never has to look back. The
// polarity is the same: the strongest positive correlation is at that lag for white noise too, which
// an inverted reference would not give. ch0 and ch1 hear the speaker alike (-34.9 dBFS at level 0.25),
// ch2 6 dB under them. One delayed copy removes only 1 to 3 dB (the driver and the enclosure filter
// what reaches the microphones); the 1024-tap canceller took 31 dB off the sweep and 15 off the noise.
var cannoliStreamRef = []int{4, 5}

var cannoliRefControls = []athenaControl{
	{"O30 I40 Switch", 1},
	{"O31 I41 Switch", 1},
}

// adcs is empty: the TLV320ADC3101 routes gain.go writes are the older Dots'. The TLV320ADC5140's gain is
// setBoardGain's.
var adcs []string

// cannoliControls wake the converter and route eTDM1's four slots (I42..I45) into UL9's first four
// channels (O26..O29), the one-to-one switches among the many the AFE offers there.
var cannoliControls = []athenaControl{
	{"ADC Reset A", 1},
	{"O26 I42 Switch", 1},
	{"O27 I43 Switch", 1},
	{"O28 I44 Switch", 1},
	{"O29 I45 Switch", 1},
}

var cannoliKernel = athenaKernel{
	micDevice:   cannoliMicDevice,
	micChannels: cannoliMicChannels,
	micUse:      Mics,
	micFull:     1 << 31,
	refDevice:   -1,
	streamRef:   cannoliStreamRef,
	refControls: cannoliRefControls,
	// softRef stands in where the loopback's routes cannot be set (an older kernel or card).
	softRef:  true,
	controls: cannoliControls,
	// The converter's analog gain on Amazon's TLV320ADC5140 driver, as athena's (athenaGainControl).
	gainControl: athenaGainControl,
	micRates:    []int{cannoliMicRate},
	refRate:     Rate,
	// 80 ms rings, as the Dot 3's: the AFE's ring sizes here are not known.
	micPeriods: 8,
	refPeriods: 8,
}

// boardKernel is the Dot 5's capture, which athenaRunning hands the paired capture.
var boardKernel = &cannoliKernel

// MediaService is the init service that holds the capture on Fire OS: Amazon's mixer daemon.
const MediaService = "mixer"

// VendorBeamformer is off: the coefficient sets the daemon reads (lib/subband) are the Dot 2's.
const VendorBeamformer = false

// vendorBeamformerSkipped is false: this board was never offered it.
const vendorBeamformerSkipped = false

// resetsOnMute is false until the mute latch has been tried with the capture open.
func resetsOnMute() bool { return false }

// openDevice is the paired capture on the speaker's card.
func openDevice() (capture, error) { return openAthena() }

// setBoardGain sets the TLV320ADC5140's analog gain per microphone (athenaGain), on the card the
// converter is linked to, the speaker's (captureDevice gives Card).
func setBoardGain(m *alsa.Mixer, db int) bool {
	athenaGain(m, db)
	return true
}
