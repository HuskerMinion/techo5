package config

import "slices"

// Netbird is the device on its owner's NetBird network (docs/netbird.md), the other way to reach it from
// away. As with Tailscale, the device's identity there is NetBird's own, kept in its state directory;
// this is whether it runs, and which management server it was joined to.
type Netbird struct {
	// On runs NetBird. Joining turns it on; it stays on until somebody turns it off.
	On bool `json:"on,omitempty"`
	// Management is the management server's URL it was joined with: NetBird's own when empty.
	Management string `json:"management,omitempty"`
	// Joined is whether it has ever been joined: then the screen offers its switch.
	Joined bool `json:"joined,omitempty"`
	// Routes are the networks the device takes routes to from the mesh, as IPv4 prefixes (feature/netbird
	// keeps them valid): only those, and none while it is empty, as it is until somebody lists one.
	Routes []string `json:"routes,omitempty"`
}

type NetbirdWriter struct{ st *Store }

func (w NetbirdWriter) On(v bool) error {
	return w.st.Update(func(c *Config) { c.Netbird.On = v })
}

// Joined is a join: on, with the management server it was made with.
func (w NetbirdWriter) Joined(management string) error {
	return w.st.Update(func(c *Config) { c.Netbird.On, c.Netbird.Management, c.Netbird.Joined = true, management, true })
}

// Routes are the networks the device takes the mesh's routes to; none turns that off.
func (w NetbirdWriter) Routes(v []string) error {
	return w.st.Update(func(c *Config) { c.Netbird.Routes = slices.Clone(v) })
}
