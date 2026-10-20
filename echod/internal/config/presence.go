package config

// Presence is the camera watching the room for somebody near (feature/presence): off on a new device.
// While it is on, Home Assistant has a Presence sensor, and with ScreenOff set the screen goes dark
// once nobody has been near for that many minutes, and lights again when somebody is.
type Presence struct {
	On bool `json:"on,omitempty"`

	// ScreenOff is minutes without anybody near before the screen goes dark; 0 leaves it on. A new
	// setting starts at DefaultPresenceScreenOff (see PresenceScreenOff).
	ScreenOff    int  `json:"screen_off,omitempty"`
	ScreenOffSet bool `json:"screen_off_set,omitempty"`

	// Sensitivity is 1 to 100; 0 is the default, 50.
	Sensitivity int `json:"sensitivity,omitempty"`

	// Gestures: a hand held over the camera stops a ring and is an event for Home Assistant.
	Gestures bool `json:"gestures,omitempty"`

	// Person: a model looks for a person in what moved (feature/person).
	Person bool `json:"person,omitempty"`

	// Faces: the face detector looks at a frame a second, for Home Assistant's Face sensor, where the
	// board has one (the 2nd gen Show 8). A face also counts as somebody near while On is set.
	Faces bool `json:"faces,omitempty"`
}

// PresenceSensitivity is the sensitivity in force.
func (p Presence) PresenceSensitivity() int {
	if p.Sensitivity <= 0 {
		return 50
	}
	return min(p.Sensitivity, 100)
}

// DefaultPresenceScreenOff is the screen's wait before anybody has chosen one.
const DefaultPresenceScreenOff = 5

// PresenceScreenOff is the wait in force: the one chosen, or the default.
func (p Presence) PresenceScreenOff() int {
	if !p.ScreenOffSet {
		return DefaultPresenceScreenOff
	}
	return p.ScreenOff
}

type PresenceWriter struct{ st *Store }

func (w PresenceWriter) On(v bool) error {
	return w.st.Update(func(c *Config) { c.Presence.On = v })
}

func (w PresenceWriter) Gestures(v bool) error {
	return w.st.Update(func(c *Config) { c.Presence.Gestures = v })
}

func (w PresenceWriter) Person(v bool) error {
	return w.st.Update(func(c *Config) { c.Presence.Person = v })
}

func (w PresenceWriter) Faces(v bool) error {
	return w.st.Update(func(c *Config) { c.Presence.Faces = v })
}

func (w PresenceWriter) Sensitivity(v int) error {
	return w.st.Update(func(c *Config) { c.Presence.Sensitivity = min(max(v, 1), 100) })
}

func (w PresenceWriter) ScreenOff(minutes int) error {
	return w.st.Update(func(c *Config) { c.Presence.ScreenOff, c.Presence.ScreenOffSet = minutes, true })
}
