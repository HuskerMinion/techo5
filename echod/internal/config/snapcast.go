package config

// Snapcast is the room as a client of a Snapcast server. Unlike Sendspin the client is the one that
// dials, so it needs the server's address: a host, or host:port when the server is not on 1704.
type Snapcast struct {
	Enabled bool   `json:"enabled,omitempty"`
	Server  string `json:"server,omitempty"`
}

type SnapcastWriter struct{ st *Store }

func (w SnapcastWriter) Enabled(v bool) error {
	return w.st.Update(func(c *Config) { c.Snapcast.Enabled = v })
}

func (w SnapcastWriter) Server(v string) error {
	return w.st.Update(func(c *Config) { c.Snapcast.Server = v })
}
