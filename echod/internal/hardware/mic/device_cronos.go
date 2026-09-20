//go:build !dot && !spot

package mic

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The Echo Show 5 2nd gen (cronos) capture path: two microphones into a TLV320AIC3101, through an
// FPGA on SPI into Amazon's amzn-mt-spi-pcm driver, opening as 16 kHz, S24_3LE, 4 channels — ch0
// and ch1 the two microphones, ch2 and ch3 the playback loopback, left then right. The two are
// distinct only once the device tree no longer says amzn,mic-downmix (tools/linux/patch-dtb.py);
// on a stock kernel the driver averages them into both slots, which is harmless with this layout.
// See docs/hardware.md, "Two microphones".
const (
	Channels      = 4
	CaptureDevice = 22

	// Mics is how many channels are microphones.
	Mics = 2

	// RefFirst is the first loopback channel and Refs how many follow it, left then right.
	RefFirst = 2
	Refs     = 2

	// CenterMic is the one the canceller and the "Center mic" mix use: the left channel.
	CenterMic = 0

	// CancelOnMix runs the echo canceller on the mix rather than the center microphone. Off here until
	// it is measured on this device; the Dot's result is in techo5-dot docs/microphones.md.
	CancelOnMix = false
)

// adcs are the converters the microphone arrives on. The card exposes a single ADC_A.
var adcs = []string{"A"}

// MediaService is the init service that holds the capture device when the daemon is not: on
// LineageOS that is the vendor audio HAL, which opens the microphone path at its own start.
const MediaService = "vendor.audio-hal"

// VendorBeamformer is off: the coefficient sets the daemon reads (lib/subband) are the Dot's, and
// the Echo Show 5's vendor partition holds a different one.
const VendorBeamformer = false

// resetsOnMute is whether the mute latch takes the converter down with it, so that the stream has to
// be opened again once the button releases it. The 1st gen Echo Show 5 does; the 2nd gen does not.
func resetsOnMute() bool { return layout.Checkers() }
