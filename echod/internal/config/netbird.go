package config

// Netbird is what the built-in NetBird client, taken out of TECHO5 for the techo5-netbird add-on (#580,
// docs/netbird.md), kept in the file. It stays because the add-on reads it: it does not start NetBird
// while On is set, and its migrate takes the management server the device was joined to (the section's
// "management", which this build has no field for and keeps as it is, unknown.go). On is the only part
// known here, for the one change made to it: turned off on the first start after the update
// (feature/netbird).
type Netbird struct {
	On bool `json:"on,omitempty"`
}

type NetbirdWriter struct{ st *Store }

// Off turns the built-in's switch off, for good: nothing here turns it on again.
func (w NetbirdWriter) Off() error {
	return w.st.Update(func(c *Config) { c.Netbird.On = false })
}
