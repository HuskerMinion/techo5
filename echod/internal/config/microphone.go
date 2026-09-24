package config

// Microphone is the array: whether it is cut, how it is combined, and how hard it is driven.
type Microphone struct {
	Muted     bool `json:"muted"`
	LEDBright bool `json:"led_bright"`

	// Gain is the analog gain on the array's converters, in dB.
	Gain int `json:"gain"`

	// Leveling brings the mix up to the level recognition expects.
	Leveling bool `json:"leveling"`

	// Mixing is how the microphones are combined. Which one wins depends on the room.
	Mixing Mixing `json:"mixing"`

	// Cancel subtracts what the speaker is playing from what the microphones hear, so a wake word
	// during a reply competes with the room rather than with the reply.
	Cancel bool `json:"cancel"`

	// CancelEngine is which canceller does it: the built-in linear filter, or WebRTC's in a helper
	// process where the image has it.
	CancelEngine CancelEngine `json:"cancel_engine,omitempty"`

	// Sensitivity is how far over the room's own floor, in dB, counts as something happening. Lower
	// notices a chair being moved; higher waits for someone to speak.
	Sensitivity int `json:"sensitivity"`

	// Denoise estimates the steady part of the room and takes it out of what the microphones heard.
	Denoise bool `json:"denoise"`

	// PipelineEnds leaves deciding when the speaker has finished to Home Assistant alone. Off, which
	// is the default, the device ends a turn itself when it hears the speaker stop.
	PipelineEnds bool `json:"pipeline_ends,omitempty"`
}

const (
	// The microphones come up live, with their LED lit. A device that came up cut, or came up cut with
	// nothing saying so, is worse than either.
	DefaultMuted     = false
	DefaultLEDBright = true

	// Analog gain on the array in dB, where the vendor ran it.
	DefaultMicGain = 20

	// Home Assistant no longer levels what a satellite sends, so the device does.
	DefaultLeveling = true

	DefaultCancel = true

	// The built-in filter. WebRTC's removes more of the music (31 dB against 27, measured on
	// cronos 2026-09-15) but its suppressor also clamps a voice talking over the music, and the
	// wake word was markedly harder to catch with it; the linear filter leaves the voice alone.
	// WebRTC stays a choice for when a clean recording matters more than the wake word.
	DefaultCancelEngine = CancelBuiltin

	// Measured on a quiet room: 0.8 dB at the 99th percentile of frames, so this is well clear of the
	// room itself and is really about brief small sounds — a chair, a keyboard.
	DefaultSensitivity = 8

	DefaultDenoise = false
)

func defaultMicrophone() Microphone {
	return Microphone{
		Muted:        DefaultMuted,
		LEDBright:    DefaultLEDBright,
		Gain:         DefaultMicGain,
		Leveling:     DefaultLeveling,
		Mixing:       DefaultMixing,
		Cancel:       DefaultCancel,
		CancelEngine: DefaultCancelEngine,
		Sensitivity:  DefaultSensitivity,
		Denoise:      DefaultDenoise,
	}
}

type MicrophoneWriter struct{ st *Store }

func (w MicrophoneWriter) Muted(v bool) error {
	return w.st.Update(func(c *Config) { c.Microphone.Muted = v })
}

func (w MicrophoneWriter) LEDBright(v bool) error {
	return w.st.Update(func(c *Config) { c.Microphone.LEDBright = v })
}

func (w MicrophoneWriter) Gain(db int) error {
	return w.st.Update(func(c *Config) { c.Microphone.Gain = db })
}

func (w MicrophoneWriter) Leveling(v bool) error {
	return w.st.Update(func(c *Config) { c.Microphone.Leveling = v })
}

func (w MicrophoneWriter) Mixing(v Mixing) error {
	return w.st.Update(func(c *Config) { c.Microphone.Mixing = v })
}

func (w MicrophoneWriter) Cancel(v bool) error {
	return w.st.Update(func(c *Config) { c.Microphone.Cancel = v })
}

func (w MicrophoneWriter) CancelEngine(v CancelEngine) error {
	return w.st.Update(func(c *Config) { c.Microphone.CancelEngine = v })
}

// CancelEngine is which echo canceller runs.
type CancelEngine string

const (
	// CancelBuiltin is the daemon's own linear filter: always there, 6% of a core while playing.
	CancelBuiltin CancelEngine = "builtin"

	// CancelWebRTC is WebRTC's canceller in the techo5-aec helper: linear filter plus a suppressor
	// and noise suppression; falls back to the built-in one on an image without the helper.
	CancelWebRTC CancelEngine = "webrtc"
)

// CancelEngines is the choice, for a select.
func CancelEngines() []CancelEngine { return []CancelEngine{CancelBuiltin, CancelWebRTC} }

func (e CancelEngine) Label() string {
	switch e {
	case CancelWebRTC:
		return "WebRTC"
	case CancelBuiltin, "":
		return "Built-in"
	}
	return string(e)
}

func (w MicrophoneWriter) Sensitivity(db int) error {
	return w.st.Update(func(c *Config) { c.Microphone.Sensitivity = db })
}

func (w MicrophoneWriter) Denoise(v bool) error {
	return w.st.Update(func(c *Config) { c.Microphone.Denoise = v })
}

// DeviceEnds is whether the device ends a turn when it hears the speaker stop.
func (w MicrophoneWriter) DeviceEnds(v bool) error {
	return w.st.Update(func(c *Config) { c.Microphone.PipelineEnds = !v })
}

// Mixing is how a microphone array is reduced to the single channel recognition reads.
type Mixing string

const (
	// MixCenter is the middle microphone alone: no arrival delay, nothing computed, and the baseline
	// anything else has to beat.
	MixCenter Mixing = "center"

	// MixAll is the plain average of every microphone: no steering, a little less of what only one
	// of them hears. On a two-microphone device it is what the vendor's driver did in the kernel.
	MixAll Mixing = "all"

	// MixDelaySum aligns all seven microphones to a steered direction and averages them.
	MixDelaySum Mixing = "delay-sum"

	// MixBeamformer is the fixed beamformer the device shipped with, whose per-band coefficients are
	// on the device and were tuned on this enclosure.
	MixBeamformer Mixing = "beamformer"
)

// Label is how the setting is shown.
func (m Mixing) Label() string {
	switch m {
	case MixCenter:
		return "Center mic"
	case MixAll:
		return "All microphones"
	case MixDelaySum:
		return "Delay and sum"
	case MixBeamformer:
		return "Beamformer"
	}
	return string(m)
}
