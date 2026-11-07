package config

// WallClock is the Echo Wall Clock this device keeps: the analog one with the 60-LED ring, which
// shows this device's timers the way an Echo shows them.
//
// Off until it is switched on. Pairing takes the Bluetooth adapter, and most devices do not have a
// clock; one that has never been switched on must not go looking for one.
type WallClock struct {
	// Enabled runs the bridge: paired, connected, and showing the timers this device holds.
	Enabled bool `json:"enabled,omitempty"`

	// Address is the clock's Bluetooth address, written down when it is paired here. Empty until a
	// clock has been paired.
	Address string `json:"address,omitempty"`
}

// WallClockWriter is the wall clock's setting.
type WallClockWriter struct{ st *Store }

// Set turns the wall clock on or off and remembers the clock's address. An empty address keeps the
// one already saved, so switching the clock off and on again does not forget it.
func (w WallClockWriter) Set(on bool, address string) error {
	return w.st.Update(func(c *Config) {
		c.WallClock.Enabled = on
		if address != "" {
			c.WallClock.Address = address
		}
	})
}
