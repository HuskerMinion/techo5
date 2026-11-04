package config

// Tailscale is the device on its owner's tailnet (docs/tailscale.md). The tailnet's own state - the
// device's identity on it - is Tailscale's, kept in its state directory; this is whether it runs, and
// which control server it was joined to.
type Tailscale struct {
	// On runs Tailscale. A device joined to a tailnet is turned on by joining, and stays on until
	// somebody turns it off.
	On bool `json:"on,omitempty"`
	// ControlURL is the control server it was joined with: Tailscale's own when empty, else the owner's
	// Headscale.
	ControlURL string `json:"control_url,omitempty"`
}

type TailscaleWriter struct{ st *Store }

func (w TailscaleWriter) On(v bool) error {
	return w.st.Update(func(c *Config) { c.Tailscale.On = v })
}

// Joined is a join: on, with the control server it was made with.
func (w TailscaleWriter) Joined(controlURL string) error {
	return w.st.Update(func(c *Config) { c.Tailscale.On, c.Tailscale.ControlURL = true, controlURL })
}
