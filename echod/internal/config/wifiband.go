package config

// WifiBand is what the device does about the band its Wi-Fi joined (feature/wifiband).
type WifiBand struct {
	// Stay is whether the device stays on the band it joined. Off is the default: the boot scripts'
	// network keeper looks every few minutes for a stronger 5 GHz radio of the network and moves the link
	// to it, which a network that steers its devices itself (a mesh) can answer with a radio the device
	// cannot join, and the link is gone until the keeper gives up. On, it neither looks nor moves.
	Stay bool `json:"stay,omitempty"`
}

type WifiBandWriter struct{ st *Store }

func (w WifiBandWriter) Stay(v bool) error {
	return w.st.Update(func(c *Config) { c.WifiBand.Stay = v })
}
