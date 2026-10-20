package config

// CamStream is the device's own camera served over RTSP (feature/camstream): whether it is, and the login
// a viewer gives. Off on a new device, and never on without a password.
type CamStream struct {
	On   bool   `json:"on,omitempty"`
	User string `json:"user,omitempty"`
	Pass string `json:"pass,omitempty"`

	// Crop makes the 2nd gen Show 8's stream the middle 1920x1088 of the sensor's frame, pixel for pixel,
	// instead of the whole view scaled to 1456x1088 (feature/camstream h264.go).
	Crop bool `json:"crop,omitempty"`
}

// DefaultCamStreamUser is the viewer's user name until one is chosen.
const DefaultCamStreamUser = "camera"

// Login is the user name and password a viewer gives.
func (c CamStream) Login() (user, pass string) {
	user = c.User
	if user == "" {
		user = DefaultCamStreamUser
	}
	return user, c.Pass
}

type CamStreamWriter struct{ st *Store }

func (w CamStreamWriter) Crop(v bool) error {
	return w.st.Update(func(c *Config) { c.CamStream.Crop = v })
}

func (w CamStreamWriter) On(v bool) error {
	return w.st.Update(func(c *Config) { c.CamStream.On = v })
}

// Login sets the viewer's user name and password; an empty password keeps the one set.
func (w CamStreamWriter) Login(user, pass string) error {
	return w.st.Update(func(c *Config) {
		c.CamStream.User = user
		if pass != "" {
			c.CamStream.Pass = pass
		}
	})
}
