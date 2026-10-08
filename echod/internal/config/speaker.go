package config

// Speaker is how loud the device is, how voice is stretched to the playback rate, and whether the
// driver's tuning is applied.
type Speaker struct {
	Volume     int        `json:"volume"`
	Resampling Resampling `json:"resampling"`
	ASP        bool       `json:"asp"`
	OutputMode OutputMode `json:"output_mode,omitempty"`

	// QuietHours is when the device makes no sound of its own, as "22-7", empty for never. See
	// quiet.go for what that does and does not cover.
	QuietHours string `json:"quiet_hours,omitempty"`

	// NightVolume is the most the device plays at through quiet hours, in volume steps, 0 for no limit.
	// DayVolume is the level it was turned down from, kept so it goes back there when the hours end,
	// 0 when it was not turned down; NightSet is the level it was turned down to, which is how a level
	// somebody chose overnight is told from the one night volume set. See media/night.go.
	NightVolume int `json:"night_volume,omitempty"`
	DayVolume   int `json:"day_volume,omitempty"`
	NightSet    int `json:"night_set,omitempty"`
	// NightOf is the night night volume last looked at as quiet hours started, the date they began
	// on, so a restart inside the same hours does not turn down a device somebody turned up since.
	NightOf string `json:"night_of,omitempty"`

	// Bass and Treble are the listener's own shelves in dB, zero for the tuning as the vendor left
	// it. They apply only while the tuning is on, since they are a stage of it (lib/asp/tone.go).
	Bass   float64 `json:"bass,omitempty"`
	Treble float64 `json:"treble,omitempty"`

	// ASPChosen is whether anybody ever set ASP themselves. Without it a saved false cannot be told
	// from a device that was never able to tune: every unit that ran a build whose tuning would not
	// load had false written back to it by the settling below, and would stay untuned forever after
	// the tuning started working. Unset means the default applies.
	ASPChosen bool `json:"asp_chosen,omitempty"`

	// ClassicSounds is TECHO5's own notes for muting and a finished timer, where the default is the
	// Home Assistant satellites' recorded ones.
	ClassicSounds bool `json:"classic_sounds,omitempty"`

	// SoundsMoved is whether the wake sounds have been moved to Home Assistant's: once, for a device
	// saved when Chirp was the default, and only for a word still on it (config.Load).
	SoundsMoved bool `json:"sounds_moved,omitempty"`
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

// moveSounds gives a device saved when Chirp was the default wake sound Home Assistant's instead,
// which is the default now: once, and only for a word (or its follow-up) still on Chirp, which is
// what nearly everyone had without choosing it. Anyone can choose Chirp again, and it then stays.
// SoundsMoved defaults to false so that a file saved before it existed, which does not mention it,
// reads as not moved; a new device, which has no file, starts moved (Load).
func (c *Config) moveSounds() {
	if c.Speaker.SoundsMoved {
		return
	}
	for i := range c.Wake.Words {
		if c.Wake.Words[i].Tone == ToneChirp {
			c.Wake.Words[i].Tone = ToneHA
		}
		if c.Wake.Words[i].FollowUpTone == ToneChirp {
			c.Wake.Words[i].FollowUpTone = ToneHA
		}
	}
	c.Speaker.SoundsMoved = true
}

type SpeakerWriter struct{ st *Store }

func (w SpeakerWriter) Volume(v int) error {
	return w.st.Update(func(c *Config) { c.Speaker.Volume = v })
}

func (w SpeakerWriter) NightVolume(v int) error {
	return w.st.Update(func(c *Config) { c.Speaker.NightVolume = v })
}

func (w SpeakerWriter) NightOf(v string) error {
	return w.st.Update(func(c *Config) { c.Speaker.NightOf = v })
}

// Night saves where night volume turned the device down from and to, together; 0, 0 when it is not
// turned down.
func (w SpeakerWriter) Night(day, set int) error {
	return w.st.Update(func(c *Config) { c.Speaker.DayVolume, c.Speaker.NightSet = day, set })
}

// Daytime is the volume the device is at outside quiet hours: the level night volume turned it down
// from while it is turned down, else the volume itself. Anything that starts from the media volume
// and is not music, like an alarm's first ring volume, starts from this, so a night limit does not
// carry into it.
func (s Speaker) Daytime() int {
	if s.DayVolume > 0 {
		return s.DayVolume
	}
	return s.Volume
}

func (w SpeakerWriter) Resampling(v Resampling) error {
	return w.st.Update(func(c *Config) { c.Speaker.Resampling = v })
}

// OutputMode saves the selected audio output mode.
func (w SpeakerWriter) OutputMode(v OutputMode) error {
	return w.st.Update(func(c *Config) { c.Speaker.OutputMode = v })
}

func (w SpeakerWriter) QuietHours(v string) error {
	return w.st.Update(func(c *Config) { c.Speaker.QuietHours = v })
}

func (w SpeakerWriter) Bass(v float64) error {
	return w.st.Update(func(c *Config) { c.Speaker.Bass = v })
}

func (w SpeakerWriter) Treble(v float64) error {
	return w.st.Update(func(c *Config) { c.Speaker.Treble = v })
}

func (w SpeakerWriter) ClassicSounds(v bool) error {
	return w.st.Update(func(c *Config) { c.Speaker.ClassicSounds = v })
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

// OutputMode selects automatic routing, the speaker, headphones when plugged in, or both at once.
type OutputMode string

const (
	OutputModeAuto      OutputMode = ""
	OutputModeSpeaker   OutputMode = "speaker"
	OutputModeHeadphone OutputMode = "headphone"
	OutputModeBoth      OutputMode = "both"
)

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
