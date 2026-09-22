//go:build dot

package mic

// The Echo Dot 2 (biscuit) capture codec accepts one format only: 16 kHz, S24_3LE, 9 channels.
const (
	Channels      = 9
	CaptureDevice = 24

	// Mics is how many of the nine channels are microphones. ch7 and ch8 are the playback loopback.
	Mics = 7

	// maxMics is the largest Mics this build has to hold, so the beamformer can size its
	// arrays at compile time. One device per build here, so it is Mics itself.
	maxMics = 7

	// RefFirst is the first loopback channel and Refs how many follow it, left then right.
	RefFirst = 7
	Refs     = 2

	// CenterMic is the middle microphone: no arrival delay relative to the array, and usable with no
	// beamformer at all.
	CenterMic = 6

	// CancelOnMix runs the echo canceller on the mix rather than the center microphone. Measured on a
	// Dot (techo5-dot docs/microphones.md, 2026-09-15): the average of seven with one canceller heard
	// speech over music 5.4 dB better than the cancelled center microphone, at the same cost.
	CancelOnMix = true
)

// adcs are the four converters the seven microphones arrive on.
var adcs = []string{"A", "B", "C", "D"}

// MediaService is the init service that holds the capture device on a fresh boot.
const MediaService = "media"

// VendorBeamformer offers the vendor's own beamformer when its coefficients are on the vendor
// partition: the sets the daemon reads (lib/subband) are the Dot's.
const VendorBeamformer = true

// resetsOnMute is false: the mute here leaves the converter alone.
func resetsOnMute() bool { return false }
