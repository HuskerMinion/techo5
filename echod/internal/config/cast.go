package config

// Cast is a phone's screen and sound sent straight to this device (feature/cast). A phone finds it
// over mDNS and connects, so there is no address to set; what it has to know is the key.
type Cast struct {
	// Enabled holds the port open. Off until somebody turns it on: a port that takes pictures and
	// sound from the network is not something to have open by default.
	Enabled bool `json:"enabled,omitempty"`

	// Key is what a phone has to prove it knows. Empty refuses every phone.
	Key string `json:"key,omitempty"`
}

type CastWriter struct{ st *Store }

func (w CastWriter) Enabled(v bool) error {
	return w.st.Update(func(c *Config) { c.Cast.Enabled = v })
}

func (w CastWriter) Key(v string) error {
	return w.st.Update(func(c *Config) { c.Cast.Key = v })
}
