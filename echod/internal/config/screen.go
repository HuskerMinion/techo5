package config

import "slices"

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

	// NightRed makes the night light a clock alone, in dim red on black, rather than the screen as it
	// is. Only with NightLight.
	NightRed bool `json:"night_red,omitempty"`

	// NightClockStyle is how the red clock looks: empty for the ordinary clock face, "led" for an LED
	// clock's seven segments, "flip" for a flip clock's cards.
	NightClockStyle string `json:"night_clock_style,omitempty"`

	// NightLightLevel is how bright the night light is, 1 to 10; none is the panel's own default.
	NightLightLevel int `json:"night_light_level,omitempty"`

	// AutoDimmest is how far auto-brightness takes a Show's screen down in a dark room, in percent of
	// Brightness, 1 to 50; none is the default (display.go).
	AutoDimmest int `json:"auto_dimmest,omitempty"`

	// Theme names the screen's palette; empty is the first one, "Custom" is Palette.
	Theme   string  `json:"theme,omitempty"`
	Palette Palette `json:"palette,omitempty"`

	// Welcomed is the first-run card having been seen and put away.
	Welcomed bool `json:"welcomed,omitempty"`

	// Clock24 shows times on the screen as 15:04 instead of 3:04 PM.
	Clock24 bool `json:"clock_24,omitempty"`

	// CameraMinutes is how long a camera opened from the screen stays up: 0 for the default minute,
	// -1 until it is tapped closed.
	CameraMinutes int `json:"camera_minutes,omitempty"`

	// AnswerSeconds is how long a voice turn's words stay on the screen after it ends: 0 for the
	// default five seconds, -1 until they are tapped away.
	AnswerSeconds int `json:"answer_seconds,omitempty"`

	// ClockPosition is where the home screen's clock sits: empty for the center, "bottom-left" or
	// "bottom-right"; DateColor the date's color, empty for the theme's (display/clock_layout.go).
	ClockPosition string `json:"clock_position,omitempty"`
	DateColor     string `json:"date_color,omitempty"`

	// ClockStyle is how the home screen's clock looks all day: empty for the classic face, or "big",
	// "flip", "led", "analog", "words", "sun", "dashboard", "binary", "world", "agenda" or "glow"
	// (display/clock_style.go). The night clock keeps its own look.
	ClockStyle string `json:"clock_style,omitempty"`

	// WorldClocks are the World style's places, as time zone names ("Europe/London"); none for its
	// own three.
	WorldClocks []string `json:"world_clocks,omitempty"`

	// NightByHA leaves the night to Home Assistant: the hours are not followed, and it is night only
	// while the Night mode switch is on. Night keeps the hours, for choosing them again.
	NightByHA bool `json:"night_by_ha,omitempty"`

	// NightOverride is the Night mode switch turned "on" or "off" at NightOverrideAt (Unix seconds).
	// With hours set it holds until they next start or end the night; with NightByHA it holds until
	// the switch is turned again.
	NightOverride   string `json:"night_override,omitempty"`
	NightOverrideAt int64  `json:"night_override_at,omitempty"`

	// TurnStyle is how a voice turn is drawn: empty for the classic title and words, "equalizer" for
	// bars moving with the voice.
	TurnStyle string `json:"turn_style,omitempty"`

	// CallButton puts a Call button on the home screen, which opens the list of devices in the house
	// and phone contacts to call. Off until somebody wants it, so an update changes nobody's screen.
	CallButton bool `json:"call_button,omitempty"`

	// WeatherStill keeps the weather page's sky still: no rain or snow falling across it. The
	// animation is on unless somebody turns it off, so a saved file without this is animated.
	WeatherStill bool `json:"weather_still,omitempty"`

	// MuteRingSubtle draws the Spot's muted ring thin and a dimmer red, for a dark room. Off until
	// somebody wants it, so an update changes nobody's screen.
	MuteRingSubtle bool `json:"mute_ring_subtle,omitempty"`

	// MusicStrip is how many seconds music plays on the full now-playing page before the Show goes
	// back to its clock with the music in a strip at the foot; none keeps the full page.
	MusicStrip int `json:"music_strip,omitempty"`

	// Language is which words the screen listens for in a turn — "en", "de", "es", "fr", "it",
	// "nl" — empty for all of them. It has nothing to do with what the assistant understands or
	// says, which is Home Assistant's pipeline; it decides only which pages a sentence brings up.
	Language string `json:"language,omitempty"`

	// ClockTap is what a tap on the clock does: empty starts a voice turn, as it always has,
	// "dashboard" puts the dashboard up, "deck" the deck (Show), and "nothing" leaves it, for a panel
	// that is talked to.
	ClockTap string `json:"clock_tap,omitempty"`

	// NoStyleSwipe stops a swipe left or right across the clock from turning its style (on unless
	// this is set; display/style_swipe.go).
	NoStyleSwipe bool `json:"no_style_swipe,omitempty"`
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

func (w ScreenWriter) NightLightLevel(v int) error {
	return w.st.Update(func(c *Config) { c.Screen.NightLightLevel = min(max(v, 0), 10) })
}

func (w ScreenWriter) AutoDimmest(v int) error {
	return w.st.Update(func(c *Config) { c.Screen.AutoDimmest = min(max(v, 0), 50) })
}

func (w ScreenWriter) NightLight(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.NightLight = v })
}

func (w ScreenWriter) NightClockStyle(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.NightClockStyle = v })
}

// AtNight sets both at once: the night light, and whether it is the red clock.
func (w ScreenWriter) AtNight(light, red bool) error {
	return w.st.Update(func(c *Config) { c.Screen.NightLight, c.Screen.NightRed = light, light && red })
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

func (w ScreenWriter) CameraMinutes(v int) error {
	return w.st.Update(func(c *Config) { c.Screen.CameraMinutes = v })
}

func (w ScreenWriter) NightByHA(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.NightByHA = v })
}

// NightOverride saves the Night mode switch: "on", "off", or "" to follow the hours again.
func (w ScreenWriter) NightOverride(v string, at int64) error {
	return w.st.Update(func(c *Config) { c.Screen.NightOverride, c.Screen.NightOverrideAt = v, at })
}

func (w ScreenWriter) ClockPosition(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.ClockPosition = v })
}

func (w ScreenWriter) ClockStyle(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.ClockStyle = v })
}

func (w ScreenWriter) WorldClocks(v []string) error {
	v = slices.Clone(v)
	return w.st.Update(func(c *Config) { c.Screen.WorldClocks = v })
}

func (w ScreenWriter) DateColor(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.DateColor = v })
}

func (w ScreenWriter) AnswerSeconds(v int) error {
	return w.st.Update(func(c *Config) { c.Screen.AnswerSeconds = v })
}

func (w ScreenWriter) TurnStyle(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.TurnStyle = v })
}

func (w ScreenWriter) WeatherStill(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.WeatherStill = v })
}

func (w ScreenWriter) MuteRingSubtle(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.MuteRingSubtle = v })
}

func (w ScreenWriter) NoStyleSwipe(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.NoStyleSwipe = v })
}

func (w ScreenWriter) CallButton(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.CallButton = v })
}

func (w ScreenWriter) MusicStrip(seconds int) error {
	return w.st.Update(func(c *Config) { c.Screen.MusicStrip = max(seconds, 0) })
}

func (w ScreenWriter) Language(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.Language = v })
}

func (w ScreenWriter) ClockTap(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.ClockTap = v })
}

// Custom saves a palette and makes it the theme.
func (w ScreenWriter) Custom(p Palette) error {
	return w.st.Update(func(c *Config) { c.Screen.Theme, c.Screen.Palette = "Custom", p })
}
