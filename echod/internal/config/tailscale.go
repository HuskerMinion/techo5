package config

// Tailscale is the device on its owner's tailnet (docs/tailscale.md). The tailnet's own state - the
// device's identity on it - is Tailscale's, kept in its state directory; this is only whether it runs.
type Tailscale struct {
	// On runs Tailscale. A device joined to a tailnet is turned on by joining, and stays on until
	// somebody turns it off.
	On bool `json:"on,omitempty"`
}

type TailscaleWriter struct{ st *Store }

func (w TailscaleWriter) On(v bool) error {
	return w.st.Update(func(c *Config) { c.Tailscale.On = v })
}
