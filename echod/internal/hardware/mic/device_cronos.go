//go:build !dot && !spot && !panel

package mic

import (
	"errors"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

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
// The 2nd gen Show 8 (athena) has four microphones as well, on a TLV320ADC5140, but its capture is two
// devices rather than one, and neither at 16 kHz: see athena.go, which reads both and hands the reader
// the crown's frame, so nothing past it knows the difference.
//
// Channels, Mics and RefFirst are variables rather than constants because one build serves all
// four screens and the board is only known at run time. Everything else here is the same on all.
// The two frames this build can find itself reading, named so they can be checked without a device
// in hand: onShow8 resolves against the board that is actually running, which on a workstation is
// always the Show 5.
const (
	show5Channels, show5Mics, show5RefFirst = 4, 2, 2
	show8Channels, show8Mics, show8RefFirst = 6, 4, 4
)

var (
	Channels = onShow8(show5Channels, show8Channels)

	// Mics is how many channels are microphones.
	Mics = onShow8(show5Mics, show8Mics)

	// RefFirst is the first loopback channel.
	RefFirst = onShow8(show5RefFirst, show8RefFirst)
)

const (
	// CaptureDevice is the one capture device on the 1st gen Show 8 and both Show 5s. athena's two
	// are in athena.go.
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

// boardKernel is nil: on this build athena's capture is chosen by the kernel it runs on
// (athenaRunning). The Echo Dot 3 build sets its own (device_donut.go).
var boardKernel *athenaKernel

// onShow8 picks the Show 8's value over the Show 5's, which covers both Show 5 generations and both
// Show 8 generations.
func onShow8[T any](show5, show8 T) T {
	if layout.Show8() {
		return show8
	}
	return show5
}

// adcs are the TLV320AIC3101 converters the microphones arrive on: a single ADC_A on the Show 5,
// ADC_A and ADC_B on the 1st gen Show 8, both confirmed present in its mixer. None on athena, whose
// converter is another part with controls of its own (athenaControls).
var adcs = func() []string {
	switch {
	case layout.Athena(), layout.Skylight():
		return nil
	case layout.Crown():
		return []string{"A", "B"}
	}
	return []string{"A"}
}()

// ErrNoMicrophone is what opening the capture says on a device built without microphones: the
// Skylight Calendar 15. Its RK809 codec has a capture path, but nothing is wired to it, so recording
// would only feed silence to the wake word. The service group logs the error and carries on without
// the microphone.
var ErrNoMicrophone = errors.New("mic: this device has no microphone")

// openDevice is the capture as the reader sees it: on athena the two devices made into one frame,
// anywhere else the one device.
func openDevice() (capture, error) {
	if layout.Skylight() {
		return nil, ErrNoMicrophone
	}
	if layout.Athena() {
		return openAthena()
	}
	return openPlain()
}

// setBoardGain sets athena's analog gain (athenaGain), and is false anywhere else, where the
// converters' PGA controls carry it.
func setBoardGain(m *alsa.Mixer, db int) bool {
	if !layout.Athena() {
		return false
	}
	athenaGain(m, db)
	return true
}

// MediaService is the init service that holds the capture device when the daemon is not: on
// LineageOS that is the vendor audio HAL, which opens the microphone path at its own start.
const MediaService = "vendor.audio-hal"

// VendorBeamformer is off: the coefficient sets the daemon reads (lib/subband) are the Dot's, and
// the Echo Show 5's vendor partition holds a different one. The Show 8 carries its own set in
// /vendor/etc/audio-algorithms, which is worth reading one day but is not the Dot's either.
const VendorBeamformer = false

// vendorBeamformerSkipped is false: no Show was ever offered it, so there is nothing to say.
const vendorBeamformerSkipped = false

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
