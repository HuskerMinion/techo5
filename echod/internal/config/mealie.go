package config

// Mealie is a Mealie recipe server this device asks for recipes.
type Mealie struct {
	URL string `json:"url,omitempty"`
	// Token is an optional long-lived access token. Some Mealie setups do not require it for authorization.
	Token string `json:"token,omitempty"`
}

// Set reports whether there is a server to ask.
func (m Mealie) Set() bool { return m.URL != "" }

// MealieWriter saves the Mealie settings.
type MealieWriter struct{ st *Store }

// Server saves the address, and the token unless token is nil: the setup page leaves a saved token
// alone when its field is left empty.
func (w MealieWriter) Server(url string, token *string) error {
	return w.st.Update(func(c *Config) {
		c.Mealie.URL = url
		if token != nil {
			c.Mealie.Token = *token
		}
	})
}
