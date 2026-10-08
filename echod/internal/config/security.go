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

	// LockPIN is the settings lock's PIN, salted and hashed (feature/security lock.go); empty for no lock.
	LockPIN string `json:"lock_pin,omitempty"`

	// LockFails and LockBlockedUntil are the wrong PINs in a row and the wait they earned, kept here so
	// that pulling the plug does not start the count over.
	LockFails        int   `json:"lock_fails,omitempty"`
	LockBlockedUntil int64 `json:"lock_blocked_until,omitempty"` // unix seconds
}

// Everything closed on a device nobody has set: SSH has no key until Home Assistant sends one, the
// web pages have no login, so they are for someone who asked for them, and a device that talks out of
// the house's cameras is one somebody chose.
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
