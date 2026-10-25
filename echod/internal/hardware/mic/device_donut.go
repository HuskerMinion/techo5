//go:build dot && donut

package mic

import (
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

// The Echo Dot 3rd gen (donut) capture path: four microphones into two TLV320ADC3101s (ADC_A and
// ADC_B; the card lists ADC_C and ADC_D as well, muted), on TDM into the MT8516's AFE, TDM_Capture
// (hw:0,1). It opens at 16 kHz, S32_LE, four channels, each converter's 24 bits left-aligned in the
// 32 (the low byte is always zero; read on unit 1, 2026-10-23). The playback reference is not in that
// stream: DL1_AWB_Record (hw:0,7) records what DL1 plays once the AFE's O05/O06 take I05/I06, as on
// the Show 8 2nd gen's mainline card. So the capture is athena's paired one (athena.go): the two
// devices read as one frame in the 1st gen Show 8's layout, the microphones then the reference, and
// with no reference device the software reference (lib/echoref).
//
// Which physical microphone each channel is has not been mapped yet (a tap test).
const (
	Channels      = 6
	CaptureDevice = donutMicDevice

	// Mics is how many channels are microphones; RefFirst the first of the two reference channels.
	Mics     = 4
	RefFirst = 4
	Refs     = 2

	// maxMics is the largest Mics this build has to hold.
	maxMics = 4

	// CenterMic: the Dot 3 has no center microphone, so ch0 stands in, as on the Show 8.
	CenterMic = 0

	// CancelOnMix runs the echo canceller on the mix rather than one microphone. Off until it is
	// measured on this device.
	CancelOnMix = false

	donutMicDevice = 1
	donutRefDevice = 7
)

// adcs are the two converters the microphones arrive on. gain.go routes their differential inputs,
// unmutes them and sets their PGA (0 to 40 dB in half-dB steps here, where the Dot 2's goes to 119).
var adcs = []string{"A", "B"}

// donutControls are the routes Fire OS's mixer_paths.xml sets before its first capture: the AIF's
// transmit side from the analog microphones (AIF TX Mux item 0, "Analog MIC") and the AFE's input from
// the internal converters (AIN Mux item 0, "INT ADC"). Without them TDM_Capture runs and every sample
// is zero.
var donutControls = []athenaControl{
	{"AIF TX Mux", 0},
	{"AIN Mux", 0},
}

// donutRefControls are the AFE interconnect from DL1's two channels into AWB's (O05/O06 from I05/I06),
// the path DL1_AWB_Record records; Fire OS's mixer_paths.xml sets the same at start. Nothing plays on
// DL2, so its (I07/I08) are let go.
var donutRefControls = []athenaControl{
	{"O05 I05 Switch", 1},
	{"O06 I06 Switch", 1},
	{"O05 I07 Switch", 0},
	{"O06 I08 Switch", 0},
}

var donutKernel = athenaKernel{
	micDevice:   donutMicDevice,
	micChannels: 4,
	micFull:     1 << 31,
	refDevice:   donutRefDevice,
	refName:     "DL1_AWB_Record",
	refControls: donutRefControls,
	controls:    donutControls,
	softRef:     true,
	micRates:    []int{Rate},
	refRate:     Rate,
	// 80 ms rings: athena's 30 ms overran here at start and after a reply (unit 1, 2026-10-23). Small
	// at 16 kHz either way (20 KB and 5 KB), and capture has none of DL1's SRAM trouble.
	micPeriods: 8,
	refPeriods: 8,
}

// boardKernel is the Dot 3's capture, which athenaRunning hands the paired capture.
var boardKernel = &donutKernel

// MediaService is the init service that holds the capture device on Fire OS.
const MediaService = "media"

// VendorBeamformer is off: the coefficient sets the daemon reads (lib/subband) are the Dot 2's seven
// microphones'.
const VendorBeamformer = false

// vendorBeamformerSkipped is false: this board was never offered it.
const vendorBeamformerSkipped = false

// resetsOnMute is false until the mute latch has been tried with the capture open.
func resetsOnMute() bool { return false }

// openDevice is the paired capture: the microphones and the playback reference made into one frame.
func openDevice() (capture, error) { return openAthena() }

// setBoardGain is false: the gain is the converters' own PGA controls (applyGain).
func setBoardGain(*alsa.Mixer, int) bool { return false }
