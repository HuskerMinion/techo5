package config

import "slices"

// Screen is the panel: whether it is lit, how brightly in percent, and whether the room's light
// is allowed to dim it below that. Only the Echo Show has one; on the Dot nothing reads this.
type Screen struct {
	// ClockHold tells Home Assistant of a finger held on the clock (feature/display, gesture_hold.go).
	ClockHold bool `json:"clock_hold,omitempty"`

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

	// NightDark puts a Spot's screen out through the night rather than dimming it; a touch lights it
	// for a while. Only without NightLight. A Show's screen goes out whenever NightLight is off.
	NightDark bool `json:"night_dark,omitempty"`

	// NightTapLifts has a tap on a Show's night light bring the screen back as by day, as a long press
	// does, until the room has been quiet a while. Off, a tap on the night light does nothing.
	NightTapLifts bool `json:"night_tap_lifts,omitempty"`

	// IgnoreRoomLights has the screen's light in Home Assistant described as a configuration entity, so
	// it stands apart from the room's lights, and the Screen power switch is what a voice names
	// (feature/display, screen_room.go). Off, the usual, the light stands with the room's lights as it
	// always did: shown with the device's controls, and handed to Assist, so "lights off" said in its
	// area puts the screen out too. Off is its zero value, so a device that never saved it is as it was
	// before the setting. (#179)
	IgnoreRoomLights bool `json:"ignore_room_lights,omitempty"`

	// NightClockStyle is how the red clock looks: empty for the ordinary clock face, "led" for an LED
	// clock's seven segments, "flip" for a flip clock's cards.
	NightClockStyle string `json:"night_clock_style,omitempty"`

	// NightWarm turns the screen's white to a lamp's through the night (screen.Tint).
	NightWarm bool `json:"night_warm,omitempty"`

	// NightLightLevel is how bright the night light is, 1 to 10; none is the panel's own default.
	NightLightLevel int `json:"night_light_level,omitempty"`

	// NightLightRescaled is whether a Spot's NightLightLevel is on the night clock's new scale, which
	// starts at backlight 3 rather than 6: set once the old one has been moved over (rescaleNightLight).
	NightLightRescaled bool `json:"night_light_rescaled,omitempty"`

	// AutoDimmest is how far auto-brightness takes a Show's screen down in a dark room, in percent of
	// Brightness, 1 to 50; none is the default (display.go).
	AutoDimmest int `json:"auto_dimmest,omitempty"`

	// Theme names the screen's palette; empty is the first one, "Custom" is Palette. It is the dark
	// mode's theme, which is the one worn unless ThemeMode says otherwise.
	Theme   string  `json:"theme,omitempty"`
	Palette Palette `json:"palette,omitempty"`

	// LightTheme is the light mode's theme, named as Theme is; empty is DefaultLightTheme. Its
	// "Custom" is LightPalette, the light mode's own, so a custom theme for each mode can be kept.
	LightTheme   string  `json:"light_theme,omitempty"`
	LightPalette Palette `json:"light_palette,omitempty"`

	// ThemeMode is which of the two the screen wears: empty for Theme, always, as before there were
	// modes; "light" for LightTheme; "auto" for the room's light to choose (display/theme_mode.go).
	ThemeMode string `json:"theme_mode,omitempty"`

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

	// ClockPosition is where the home screen's clock sits: empty for the center, "bottom-left",
	// "bottom-center" or "bottom-right" on a Show, "bottom" on the Spot; DateColor the date's color,
	// empty for the theme's (display/clock_position.go, clock_layout.go).
	ClockPosition string `json:"clock_position,omitempty"`
	DateColor     string `json:"date_color,omitempty"`
	// WeatherPosition is where the home screen's weather sits: empty for the top left, "top-right"
	// (display/clock_layout.go). The Show's only.
	WeatherPosition string `json:"weather_position,omitempty"`

	// ClockStyle is how the home screen's clock looks all day: empty for the classic face, or "big",
	// "flip", "led", "analog", "words", "sun", "dashboard", "binary", "world", "agenda", "glow", "nixie",
	// "stack" or "overlap" (display/clock_style.go). The night clock keeps its own look.
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

	// TurnStyle is how a voice turn is drawn: empty for the classic title and words, "wave" for a wave
	// moving with the voice, "equalizer" for bars, "vu" for a pair of needle meters, "orb" for a ball of
	// light, "hal" for the HAL 9000 computer's eye, or "cue" and "captions" for a light over the page
	// left up, with the words as captions for the second.
	TurnStyle string `json:"turn_style,omitempty"`

	// TimerStyle is how running timers show on the Show's clock: empty for the line under the date,
	// "ring", "corner", "banner" or "big".
	TimerStyle string `json:"timer_style,omitempty"`

	// Skin is a whole look for the screen over the theme and the font, on the Show, the Spot and the
	// Panel app: empty for none, "pipboy" for the green phosphor terminal, "amber" for the amber one,
	// "lcars" for the starship console, "eink" for ink on paper, "blueprint", "gameboy" for the
	// handheld's four greens, "matrix", "synthwave" or "hal9000".
	Skin string `json:"skin,omitempty"`

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

	// Language is which words the screen listens for in a turn — "en", "de", "es", "fr", "it", "nl",
	// "pt", "ru" — empty for all of them. It has nothing to do with what the assistant understands or
	// says, which is Home Assistant's pipeline; it decides only which pages a sentence brings up.
	Language string `json:"language,omitempty"`

	// ClockTap is what a tap on the clock does: empty starts a voice turn, as it always has,
	// "dashboard" puts the dashboard up, "deck" the deck (Show), and "nothing" leaves it, for a panel
	// that is talked to.
	ClockTap string `json:"clock_tap,omitempty"`

	// NoStyleSwipe stops a swipe left or right across the clock from turning its style (on unless
	// this is set; display/style_swipe.go).
	NoStyleSwipe bool `json:"no_style_swipe,omitempty"`

	// NoVolumeSwipe stops a swipe up or down on the clock from changing the volume (on unless this is
	// set; display/volume_swipe.go, #243).
	NoVolumeSwipe bool `json:"no_volume_swipe,omitempty"`

	// Font is the font the screen's words are drawn in: empty for Go, the screen's own, or one of the
	// others lib/fonts offers ("inter", "comicneue", ...). One this build does not have is Go.
	Font string `json:"font,omitempty"`
}

// DefaultTheme is the palette a new device comes up in.
const DefaultTheme = "Ember"

// DefaultLightTheme is the light mode's theme until another is chosen.
const DefaultLightTheme = "Paper"

// The theme modes ThemeMode may be; the dark mode is empty.
const (
	ThemeModeLight = "light"
	ThemeModeAuto  = "auto"
)

// ThemeFor is the name of the light mode's theme, or the dark mode's.
func (s Screen) ThemeFor(light bool) string {
	switch {
	case !light:
		return s.Theme
	case s.LightTheme == "":
		return DefaultLightTheme
	}
	return s.LightTheme
}

// PaletteFor is the light mode's custom palette, or the dark mode's.
func (s Screen) PaletteFor(light bool) Palette {
	if light {
		return s.LightPalette
	}
	return s.Palette
}

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

// ThemeFor saves the light mode's theme, or the dark mode's.
func (w ScreenWriter) ThemeFor(light bool, v string) error {
	if !light {
		return w.Theme(v)
	}
	return w.st.Update(func(c *Config) { c.Screen.LightTheme = v })
}

// ThemeMode saves which theme the screen wears: "", ThemeModeLight or ThemeModeAuto.
func (w ScreenWriter) ThemeMode(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.ThemeMode = v })
}

// Themes saves both modes' themes and the mode at once, as the setup page sets them.
func (w ScreenWriter) Themes(dark, light, mode string) error {
	return w.st.Update(func(c *Config) { c.Screen.Theme, c.Screen.LightTheme, c.Screen.ThemeMode = dark, light, mode })
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

func (w ScreenWriter) NightTapLifts(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.NightTapLifts = v })
}

// IgnoreRoomLights is taken back when it could not be saved, since a change to it asks for a reconnect
// that a change not kept must not get, nor carry to the next one saved.
func (w ScreenWriter) IgnoreRoomLights(v bool) error {
	return w.st.UpdateOrUndo(func(c *Config) { c.Screen.IgnoreRoomLights = v })
}

func (w ScreenWriter) NightClockStyle(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.NightClockStyle = v })
}

func (w ScreenWriter) NightWarm(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.NightWarm = v })
}

// AtNight sets both at once: the night light, and whether it is the red clock. NightDark is cleared.
func (w ScreenWriter) AtNight(light, red bool) error {
	return w.AtNightDark(light, red, false)
}

// AtNightDark sets all three at once: the night light, whether it is the red clock, and on the Spot
// whether the night puts the screen out.
func (w ScreenWriter) AtNightDark(light, red, dark bool) error {
	return w.st.Update(func(c *Config) {
		c.Screen.NightLight, c.Screen.NightRed, c.Screen.NightDark = light, light && red, !light && dark
	})
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

func (w ScreenWriter) Font(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.Font = v })
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

func (w ScreenWriter) WeatherPosition(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.WeatherPosition = v })
}

func (w ScreenWriter) AnswerSeconds(v int) error {
	return w.st.Update(func(c *Config) { c.Screen.AnswerSeconds = v })
}

func (w ScreenWriter) TurnStyle(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.TurnStyle = v })
}

func (w ScreenWriter) TimerStyle(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.TimerStyle = v })
}

func (w ScreenWriter) Skin(v string) error {
	return w.st.Update(func(c *Config) { c.Screen.Skin = v })
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

func (w ScreenWriter) NoVolumeSwipe(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.NoVolumeSwipe = v })
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

// CustomFor saves the light mode's custom palette, or the dark mode's, and makes it that mode's theme.
// The other mode's is left as it is.
func (w ScreenWriter) CustomFor(light bool, p Palette) error {
	return w.st.Update(func(c *Config) {
		if light {
			c.Screen.LightPalette, c.Screen.LightTheme = p, "Custom"
		} else {
			c.Screen.Palette, c.Screen.Theme = p, "Custom"
		}
	})
}

// ClockHold is whether a finger held on the clock is told to Home Assistant.
func (w ScreenWriter) ClockHold(v bool) error {
	return w.st.Update(func(c *Config) { c.Screen.ClockHold = v })
}
