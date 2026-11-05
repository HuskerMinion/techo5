package config

// Security is what the device opens to the network besides its link to Home Assistant: an SSH
// server, and the camera and screen pages on the web port. Home Assistant's own link is encrypted
// with the device key and is not a setting.
type Security struct {
	SSH    bool `json:"ssh"`
	Camera bool `json:"camera_web"`
	Screen bool `json:"screen_web"`

	// TalkBack lets the camera page send the microphones to a camera's speaker (config.TalkBack).
	TalkBack bool `json:"talk_back,omitempty"`

	// Takes lets the setup page record takes of a word on the microphones (feature/takes), each
	// recording started by a press on the device.
	Takes bool `json:"takes,omitempty"`

	// Baby lets the baby monitor page play the room's microphone audio live to a browser given the link
	// (feature/babymon). BabyKey is the secret in that link: made when the switch is first turned on,
	// kept while it is off, and replaced from the setup page. Both are this device's own (KeepOwn), and
	// the key is a secret a settings file neither carries nor changes.
	Baby    bool   `json:"baby,omitempty"`
	BabyKey string `json:"baby_key,omitempty"`

	// BabyLight is how the device shows that somebody is listening to the baby monitor: "" as it always
	// did, BabyLightDim at the least light that still shows it, or BabyLightOff not at all, for a
	// nursery kept dark. The device's own, like Baby: a settings file neither carries nor changes it.
	BabyLight string `json:"baby_light,omitempty"`

	// WordsKept keeps the words of a voice request on the device: Last heard and Last reply stay empty,
	// and the voice events leave out what was heard and the reply (feature/activity). Off, as it always
	// was, since dashboards show Last heard.
	WordsKept bool `json:"words_kept,omitempty"`

	// LockPIN is the settings lock's PIN, salted and hashed (feature/security lock.go); empty for no lock.
	LockPIN string `json:"lock_pin,omitempty"`

	// LockFails and LockBlockedUntil are the wrong PINs in a row and the wait they earned, kept here so
	// that pulling the plug does not start the count over.
	LockFails        int   `json:"lock_fails,omitempty"`
	LockBlockedUntil int64 `json:"lock_blocked_until,omitempty"` // unix seconds
}

// Everything closed on a device nobody has set: SSH has no key until Home Assistant sends one, the
// web pages have no login, so they are for someone who asked for them, and a device that talks out of
// the house's cameras, or records takes of a word, is one somebody chose.
func defaultSecurity() Security { return Security{} }

type SecurityWriter struct{ st *Store }

func (w SecurityWriter) SSH(v bool) error {
	return w.st.Update(func(c *Config) { c.Security.SSH = v })
}

func (w SecurityWriter) Camera(v bool) error {
	return w.st.Update(func(c *Config) { c.Security.Camera = v })
}

func (w SecurityWriter) Screen(v bool) error {
	return w.st.Update(func(c *Config) { c.Security.Screen = v })
}

func (w SecurityWriter) LockPIN(v string) error {
	return w.st.Update(func(c *Config) { c.Security.LockPIN = v })
}

func (w SecurityWriter) LockTries(fails int, blockedUntil int64) error {
	return w.st.Update(func(c *Config) { c.Security.LockFails, c.Security.LockBlockedUntil = fails, blockedUntil })
}

func (w SecurityWriter) TalkBack(v bool) error {
	return w.st.Update(func(c *Config) { c.Security.TalkBack = v })
}

func (w SecurityWriter) Takes(v bool) error {
	return w.st.Update(func(c *Config) { c.Security.Takes = v })
}

func (w SecurityWriter) Baby(v bool) error {
	return w.st.Update(func(c *Config) { c.Security.Baby = v })
}

// What BabyLight may be.
const (
	BabyLightNormal = ""
	BabyLightDim    = "dim"
	BabyLightOff    = "off"
)

func (w SecurityWriter) BabyLight(v string) error {
	return w.st.Update(func(c *Config) { c.Security.BabyLight = v })
}

func (w SecurityWriter) BabyKey(v string) error {
	return w.st.Update(func(c *Config) { c.Security.BabyKey = v })
}

func (w SecurityWriter) WordsKept(v bool) error {
	return w.st.Update(func(c *Config) { c.Security.WordsKept = v })
}
