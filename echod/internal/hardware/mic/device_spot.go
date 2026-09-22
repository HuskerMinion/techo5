//go:build spot

package mic

// The Echo Spot (rook) capture path: four microphones on two TLV320AIC3101 converters (ADC_A and
// ADC_B), through an FPGA on SPI into amzn-mt-spi-pcm. The 4-mic kernel build fixes the frame at six
// channels, so the device opens only as 16 kHz, S24_3LE, 6 channels: ch0 to ch3 the microphones, ch4
// and ch5 the playback loopback, left then right. Measured on LineageOS 18.1 (techo5-spot
// docs/hardware.md): a room at -68 dBFS on every microphone, a 0.1 FS tone at -27 dBFS on the loopback.
const (
	Channels      = 6
	CaptureDevice = 22

	// Mics is how many channels are microphones.
	Mics = 4

	// maxMics is the largest Mics this build has to hold, so the beamformer can size its
	// arrays at compile time. One device per build here, so it is Mics itself.
	maxMics = 4

	// RefFirst is the first loopback channel and Refs how many follow it, left then right.
	RefFirst = 4
	Refs     = 2

	// CenterMic is the one the canceller and the "Center mic" mix use. The Spot has no center
	// microphone; ch0 stands in until the array is mapped.
	CenterMic = 0

	// CancelOnMix runs the echo canceller on the mix rather than one microphone. Off until measured
	// on this device.
	CancelOnMix = false
)

// adcs are the converters the microphones arrive on.
var adcs = []string{"A", "B"}

// MediaService is the init service that holds the capture device when the daemon is not: on
// LineageOS, the vendor audio HAL.
const MediaService = "vendor.audio-hal"

// VendorBeamformer is off: the coefficient sets the daemon reads (lib/subband) are the Dot's, and
// the Echo Spot's vendor partition holds a different one.
const VendorBeamformer = false

// resetsOnMute is false: the mute here leaves the converter alone.
func resetsOnMute() bool { return false }
