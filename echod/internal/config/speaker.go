package config

// Speaker is how loud the device is, how voice is stretched to the playback rate, and whether the
// driver's tuning is applied.
type Speaker struct {
	Volume     int        `json:"volume"`
	Resampling Resampling `json:"resampling"`
	ASP        bool       `json:"asp"`

	// QuietHours is when the device makes no sound of its own, as "22-7", empty for never. See
	// quiet.go for what that does and does not cover.
	QuietHours string `json:"quiet_hours,omitempty"`

	// ASPChosen is whether anybody ever set ASP themselves. Without it a saved false cannot be told
	// from a device that was never able to tune: every unit that ran a build whose tuning would not
	// load had false written back to it by the settling below, and would stay untuned for ever after
	// the tuning started working. Unset means the default applies.
	ASPChosen bool `json:"asp_chosen,omitempty"`
}

const (
	// VolumeSteps runs 0..30, the range Android gives STREAM_MUSIC and the one the vendor's volume
	// curves are indexed by, so a step here is a step there. Home Assistant works in 0..1.
	VolumeSteps = 30

	// Half way up, so a device nobody has turned up is audible without being startling.
	DefaultVolume = VolumeSteps / 2

	DefaultResampling = ResampleSinc

	// DefaultASP applies the driver's tuning, which is what the vendor's firmware does.
	DefaultASP = true
)

func defaultSpeaker() Speaker {
	return Speaker{Volume: DefaultVolume, Resampling: DefaultResampling, ASP: DefaultASP}
}

type SpeakerWriter struct{ st *Store }

func (w SpeakerWriter) Volume(v int) error {
	return w.st.Update(func(c *Config) { c.Speaker.Volume = v })
}

func (w SpeakerWriter) Resampling(v Resampling) error {
	return w.st.Update(func(c *Config) { c.Speaker.Resampling = v })
}

func (w SpeakerWriter) QuietHours(v string) error {
	return w.st.Update(func(c *Config) { c.Speaker.QuietHours = v })
}

// ASPWanted is what the tuning should be set to: what somebody chose, or the default until somebody
// does. It is what they asked for, never what the device managed, so a device that could not tune
// yesterday tunes today without anybody touching it.
func (s Speaker) ASPWanted() bool {
	if !s.ASPChosen {
		return DefaultASP
	}
	return s.ASP
}

// ASP records what somebody asked for, and that they asked. What the device managed is not saved:
// see ASPWanted.
func (w SpeakerWriter) ASP(v bool) error {
	return w.st.Update(func(c *Config) { c.Speaker.ASP, c.Speaker.ASPChosen = v, true })
}

// Resampling is how the 16 kHz voice a pipeline sends is stretched to the 48 kHz the codec takes.
type Resampling string

const (
	// ResampleSinc interpolates through a low pass at the input's Nyquist, which is the correct
	// answer and what the device uses unless told otherwise.
	ResampleSinc Resampling = "sinc"

	// ResampleLinear draws a straight line between input samples. It attenuates the images rather
	// than removing them, for a fraction of the work.
	ResampleLinear Resampling = "linear"

	// ResampleHold repeats each input sample, which is what the device did before any of this and
	// leaves the images in full.
	ResampleHold Resampling = "hold"
)

// Label is how the setting is shown.
func (r Resampling) Label() string {
	switch r {
	case ResampleSinc:
		return "Band limited"
	case ResampleLinear:
		return "Linear"
	case ResampleHold:
		return "Repeat samples"
	}
	return string(r)
}
