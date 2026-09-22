//go:build !dot && !spot

package mic

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The Echo Show 5 2nd gen (cronos) capture path: two microphones into a TLV320AIC3101, through an
// FPGA on SPI into Amazon's amzn-mt-spi-pcm driver, opening as 16 kHz, S24_3LE, 4 channels — ch0
// and ch1 the two microphones, ch2 and ch3 the playback loopback, left then right. The two are
// distinct only once the device tree no longer says amzn,mic-downmix (tools/linux/patch-dtb.py);
// on a stock kernel the driver averages them into both slots, which is harmless with this layout.
// See docs/hardware.md, "Two microphones".
//
// The Echo Show 8 (crown) is the same path with the Echo Spot's array on the end of it: four
// microphones across two converters, ADC_A and ADC_B, on the same capture device, opening as
// 16 kHz, S24_3LE, 6 channels — ch0 to ch3 the microphones, ch4 and ch5 the loopback. Measured on a
// unit 2026-09-22 with cmd/audioprobe: all four microphones read about -51 dBFS on a quiet room and
// the loopback carried a 1 kHz tone at -16.5 dBFS, with no overruns at the daemon's own period.
// idme carries miccal.0 to miccal.3, which agrees on four.
//
// Channels, Mics and RefFirst are variables rather than constants because one build serves all
// three screens and the board is only known at run time. Everything else here is the same on both.
// The two frames this build can find itself reading, named so they can be checked without a device
// in hand: onCrown resolves against the board that is actually running, which on a workstation is
// always the Show 5.
const (
	show5Channels, show5Mics, show5RefFirst = 4, 2, 2
	show8Channels, show8Mics, show8RefFirst = 6, 4, 4
)

var (
	Channels = onCrown(show5Channels, show8Channels)

	// Mics is how many channels are microphones.
	Mics = onCrown(show5Mics, show8Mics)

	// RefFirst is the first loopback channel.
	RefFirst = onCrown(show5RefFirst, show8RefFirst)
)

const (
	CaptureDevice = 22

	// maxMics is the largest Mics this build has to hold, so the beamformer can size its arrays at
	// compile time and count microphones at run time.
	maxMics = 4

	// Refs is how many loopback channels follow RefFirst, left then right. Two on both.
	Refs = 2

	// CenterMic is the one the canceller and the "Center mic" mix use: the left channel. The Show 8
	// has no center microphone either; ch0 stands in until its array is mapped, as on the Spot.
	CenterMic = 0

	// CancelOnMix runs the echo canceller on the mix rather than the center microphone. Off here until
	// it is measured on this device; the Dot's result is in techo5-dot docs/microphones.md.
	CancelOnMix = false
)

// onCrown picks the Show 8's value over the Show 5's, which covers both Show 5 generations.
func onCrown[T any](show5, show8 T) T {
	if layout.Crown() {
		return show8
	}
	return show5
}

// adcs are the converters the microphones arrive on: a single ADC_A on the Show 5, ADC_A and ADC_B
// on the Show 8, both confirmed present in its mixer.
var adcs = onCrown([]string{"A"}, []string{"A", "B"})

// MediaService is the init service that holds the capture device when the daemon is not: on
// LineageOS that is the vendor audio HAL, which opens the microphone path at its own start.
const MediaService = "vendor.audio-hal"

// VendorBeamformer is off: the coefficient sets the daemon reads (lib/subband) are the Dot's, and
// the Echo Show 5's vendor partition holds a different one. The Show 8 carries its own set in
// /vendor/etc/audio-algorithms, which is worth reading one day but is not the Dot's either.
const VendorBeamformer = false

// resetsOnMute is whether the mute latch takes the converter down with it, so that the stream has to
// be opened again once the button releases it. The 1st gen Echo Show 5 does; the 2nd gen does not.
//
// The Show 8 does, and worse: measured on a unit 2026-09-22, engaging the latch makes every
// microphone read digital zeros, and releasing it with the button does not bring them back. Opening
// a fresh stream does not either, and neither does rewriting the ADC controls the long way round.
// Only a reboot restores them. That is the checkers fault exactly, so a Show 8 also needs
// tools/linux/patches/checkers-0001-mic-enable-on-capture.patch in its kernel build; until it has
// one, handing the device back is necessary but not sufficient.
func resetsOnMute() bool { return layout.Checkers() || layout.Crown() }
