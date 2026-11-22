package config

// Plex is the device as a Plex player (feature/plexcast): the account it is linked to, the token it
// plays with, and the identifier Plex knows it by. A device nobody has linked is a device that
// advertises nothing, opens no port and registers nothing, so every one of these being empty is the
// ordinary state of a new device.
type Plex struct {
	// Token is the account's access token, from the four-character PIN somebody typed at
	// plex.tv/link. It is this section's one secret: never logged, never shown on a page, and left out
	// of an exported settings file (export.go).
	Token string `json:"plex_token,omitempty"`

	// User is the account's name as plex.tv gives it, which is what the settings screen and the setup
	// page say once the device is linked. Empty where plex.tv returned none.
	User string `json:"plex_user,omitempty"`

	// UserID is the account's own number. The cloud's PubSub address needs it, between the host and
	// this device's identifier: wss://pubsub.plex.tv/sub/websockets/{user}/{clientIdentifier}.
	UserID string `json:"plex_user_id,omitempty"`

	// ClientID is the identifier made at random the first time it is needed and kept: plex.tv binds the
	// token to it, so a new one would silently invalidate the link. It is also what Plex shows as this
	// device's address, and what every controller dedupes on.
	ClientID string `json:"plex_client_id,omitempty"`
}

// Linked is whether an account is linked: the one thing every part of plexcast asks first.
func (p Plex) Linked() bool { return p.Token != "" }

type PlexWriter struct{ st *Store }

// ClientID remembers the device's identifier, made once.
func (w PlexWriter) ClientID(v string) error {
	return w.st.Update(func(c *Config) { c.Plex.ClientID = v })
}

// Link is a finished PIN flow: the token, and who it belongs to. A user plex.tv did not name is stored
// empty rather than guessed at, and the pages say "Linked" for it.
func (w PlexWriter) Link(token, user, userID string) error {
	return w.st.Update(func(c *Config) {
		c.Plex.Token, c.Plex.User, c.Plex.UserID = token, user, userID
	})
}

// Unlink forgets the account. The identifier stays: it is the device's name, not the account's, and
// keeping it means a device linked again is the same player to Plex and to the controllers that saw
// it before.
func (w PlexWriter) Unlink() error {
	return w.st.Update(func(c *Config) {
		c.Plex.Token, c.Plex.User, c.Plex.UserID = "", "", ""
	})
}
