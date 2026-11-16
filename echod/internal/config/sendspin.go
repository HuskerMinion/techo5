package config

// Sendspin is the room's part of a synchronized stream. A server discovers it and dials in, so there
// is no address to set.
type Sendspin struct {
	Enabled bool `json:"enabled"`
	// DelayMs holds this room back by that many milliseconds against the server's schedule, so it can
	// match a player that is slower than it (one with a wired chain of its own). 0 is the schedule as
	// sent.
	DelayMs int `json:"delay_ms,omitempty"`
}

// MaxSendspinDelayMs is the longest a room is held back.
const MaxSendspinDelayMs = 1000

// SendspinDelay is ms as a delay can be set: 0 to MaxSendspinDelayMs.
func SendspinDelay(ms int) int { return min(max(ms, 0), MaxSendspinDelayMs) }

func defaultSendspin() Sendspin { return Sendspin{Enabled: true} }

type SendspinWriter struct{ st *Store }

func (w SendspinWriter) DelayMs(ms int) error {
	ms = SendspinDelay(ms)
	return w.st.Update(func(c *Config) { c.Sendspin.DelayMs = ms })
}

func (w SendspinWriter) Enabled(v bool) error {
	return w.st.Update(func(c *Config) { c.Sendspin.Enabled = v })
}
