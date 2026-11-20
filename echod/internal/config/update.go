package config

// Update is which releases the device follows. The channel is a name rather than a URL: where each
// one points is compiled in, so this cannot be used to send the device somewhere else for its next
// binary.
type Update struct {
	Channel string `json:"channel"`

	// LastVersion is the build Home Assistant was last told about. It moves only once the telling has
	// happened, so a version that changed while nothing was listening is still reported later.
	LastVersion string `json:"last_version"`

	// AutoInstall installs an update the channel offers by itself, overnight while nothing is playing,
	// for a device nobody is going to press Install on: one with no Home Assistant, far away.
	AutoInstall bool `json:"auto_install,omitempty"`

	// HeldBack is the release a rollback (Home Assistant's Roll back button) went back from. Installing
	// updates by themselves leaves it, and anything no newer, alone, so the same release is not put back
	// the next night; one newer than it is installed as usual. Pressing Install still installs it.
	HeldBack string `json:"held_back,omitempty"`
}

const DefaultChannel = "stable"

func defaultUpdate() Update {
	return Update{Channel: DefaultChannel}
}

type UpdateWriter struct{ st *Store }

func (w UpdateWriter) Channel(v string) error {
	return w.st.Update(func(c *Config) { c.Update.Channel = v })
}

func (w UpdateWriter) AutoInstall(v bool) error {
	return w.st.Update(func(c *Config) { c.Update.AutoInstall = v })
}

func (w UpdateWriter) LastVersion(v string) error {
	return w.st.Update(func(c *Config) { c.Update.LastVersion = v })
}

func (w UpdateWriter) HeldBack(v string) error {
	return w.st.Update(func(c *Config) { c.Update.HeldBack = v })
}
