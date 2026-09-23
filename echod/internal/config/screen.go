package config

// Screen is the panel: whether it is lit, how brightly in percent, and whether the room's light
// is allowed to dim it below that. Only the Echo Show has one; on the Dot nothing reads this.
type Screen struct {
	On         bool `json:"on"`
	Brightness int  `json:"brightness"`
	Auto       bool `json:"auto"`

	// Night is when the screen goes dark on its own, as "22-6" (from 22:00 to 06:00); empty
	// never. A tap wakes it for a while.
	Night string `json:"night,omitempty"`

	// NightLight keeps a Show's screen on at a faint glow through the night instead of putting it out;
	// a touch brings it up to its brightness for a while.
	NightLight bool `json:"night_light,omitempty"`

	// Theme names the screen's palette; empty is the first one, "Custom" is Palette.
	Theme   string  `json:"theme,omitempty"`
	Palette Palette `json:"palette,omitempty"`

	// Welcomed is the first-run card having been seen and put away.
	Welcomed bool `json:"welcomed,omitempty"`

	// Clock24 shows times on the screen as 15:04 instead of 3:04 PM.
	Clock24 bool `json:"clock_24,omitempty"`

	// MusicStrip is how many seconds music plays on the full now-playing page before the Show goes
	// back to its clock with the music in a strip at the foot; none keeps the full page.
	MusicStrip int `json:"music_strip,omitempty"`

	// Language is which words the screen listens for in a turn — "en", "de", "es", "fr", "it",
	// "nl" — empty for all of them. It has nothing to do with what the assistant understands or
	// says, which is Home Assistant's pipeline; it decides only which pages a sentence brings up.
	Language string `json:"language,omitempty"`
}

// DefaultTheme is the palette a new device comes up in.
const DefaultTheme = "Ember"

// Palette is a custom theme's five colors, as #rrggbb.
type Palette struct {
	Ground string `json:"ground,omitempty"`
	Accent string `json:"accent,omitempty"`
	Text   string `json:"text,omitempty"`
	Dim    string `json:"dim,omitempty"`
	Rules  string `json:"rules,omitempty"`
}

// DefaultScreenBrightness is comfortable on a desk in a lit room; the panel's own top is glaring.
const DefaultScreenBrightness = 60

func defaultScreen() Screen {
	return Screen{On: true, Brightness: DefaultScreenBrightness, Auto: true, Theme: DefaultTheme}
}

type ScreenWriter struct{ st *Store }

func (w ScreenWriter) On(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.On = v })
}

func (w ScreenWriter) Brightness(v int) error {
	return w.st.Update(func(c *Config) { c.Screen.Brightness = v })
}

func (w ScreenWriter) Auto(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.Auto = v })
}

func (w ScreenWriter) Theme(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.Theme = v })
}

func (w ScreenWriter) NightLight(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.NightLight = v })
}

func (w ScreenWriter) Night(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.Night = v })
}

func (w ScreenWriter) Welcomed(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.Welcomed = v })
}

func (w ScreenWriter) Clock24(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.Clock24 = v })
}

func (w ScreenWriter) MusicStrip(seconds int) error {
	return w.st.Update(func(c *Config) { c.Screen.MusicStrip = max(seconds, 0) })
}

func (w ScreenWriter) Language(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.Language = v })
}

// Custom saves a palette and makes it the theme.
func (w ScreenWriter) Custom(p Palette) error {
	return w.st.Update(func(c *Config) { c.Screen.Theme, c.Screen.Palette = "Custom", p })
}
