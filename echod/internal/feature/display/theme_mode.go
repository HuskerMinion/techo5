//go:build !dot && !spot

package display

import (
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/ambient"
)

// Light and dark mode: the screen has a theme for each, the dark one being Theme as it always was and
// the light one Paper until another is chosen, and the mode says which it wears. Dark, the default,
// is the screen as it was before there were modes. Auto lets the room's light choose, from the same
// sensor auto-brightness reads: a lit room puts the light theme on, a dim one takes it off, each only
// once the room has stayed that way a while, so a lamp switched or a shadow passing does not flip it.
// The night clock keeps its own look whatever the mode.

const (
	// themeLightLux is a room bright enough for the light theme, and themeDarkLux one dim enough to go
	// back to the dark one. The gap between them keeps a room at the edge from flipping.
	themeLightLux = 120.0
	themeDarkLux  = 30.0

	// themeHold is how long the room has to stay past one of them before the theme follows.
	themeHold = time.Minute
)

// themeModes are the modes as Home Assistant and the setup page name them, with what the config keeps.
var themeModes = []struct{ label, value string }{
	{"Dark", ""},
	{"Light", config.ThemeModeLight},
	{"Auto", config.ThemeModeAuto},
}

// hasLightSensor is whether the room's light can choose: not on a panel (ambient.Here). A variable for
// the tests.
var hasLightSensor = func() bool { return ambient.Here }

// themeModesHere are the modes this device offers: Auto only where there is a light sensor.
func themeModesHere() []struct{ label, value string } {
	if hasLightSensor() {
		return themeModes
	}
	return themeModes[:2]
}

// themeModeIndex is the mode in force, in themeModesHere; one the file does not know is Dark, and so is
// Auto on a device with no light sensor, one shared from another device.
func themeModeIndex() int {
	m := config.Get().Screen.ThemeMode
	for i, o := range themeModesHere() {
		if o.value == m {
			return i
		}
	}
	return 0
}

// roomTheme is Auto's choice from the room's light: whether the room is lit, and since when a reading
// on the other side of it has stood.
type roomTheme struct {
	mu    sync.Mutex
	known bool
	lit   bool
	since time.Time
}

var room roomTheme

// step takes the room's light now and reports whether the room is lit. The first reading decides at
// once, so a device started in a lit room does not wear the dark theme for a minute first; after that
// a reading past the other threshold has to hold themeHold.
func (rt *roomTheme) step(lux float64, now time.Time) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !rt.known {
		rt.known, rt.lit = true, lux >= themeLightLux
		return rt.lit
	}
	across := rt.lit && lux < themeDarkLux || !rt.lit && lux >= themeLightLux
	switch {
	case !across:
		rt.since = time.Time{}
	case rt.since.IsZero():
		rt.since = now
	case now.Sub(rt.since) >= themeHold:
		rt.lit, rt.since = !rt.lit, time.Time{}
		slog.Info("theme mode: the room's light chose a theme", "light", rt.lit, "lux", lux)
	}
	return rt.lit
}

// forget drops the room's choice, so the next reading decides at once: Auto chosen again later starts
// from the room as it is then, not as it was when Auto was last on.
func (rt *roomTheme) forget() {
	rt.mu.Lock()
	rt.known, rt.lit, rt.since = false, false, time.Time{}
	rt.mu.Unlock()
}

// isLit is the room's last choice, without a new reading: dark until there has been one.
func (rt *roomTheme) isLit() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.lit
}

// followRoom gives Auto the room's light now. Called with every frame: a steady room sends no
// readings, and the hold has to run out all the same. A screen with no reading yet stays as it is.
func followRoom(now time.Time) {
	if config.Get().Screen.ThemeMode != config.ThemeModeAuto {
		room.forget()
		return
	}
	if lux, _, ok := ambient.Get().Current(); ok {
		room.step(lux, now)
	}
}

// lightShown is whether the screen wears its light theme: the Light mode, or Auto in a lit room.
func lightShown(sc config.Screen) bool {
	switch sc.ThemeMode {
	case config.ThemeModeLight:
		return true
	case config.ThemeModeAuto:
		return hasLightSensor() && room.isLit()
	}
	return false
}

// lightThemeShown is whether the light theme is the one in force, for the setup page, which wears it too.
func lightThemeShown() bool { return lightShown(config.Get().Screen) }

// themeModeSelect is Theme mode in Home Assistant: Dark, Light or Auto, without Auto where there is no
// light sensor. Theme is the dark mode's theme and Light theme the light mode's, whichever is worn.
func themeModeSelect(wake func()) *esphome.Select {
	here := themeModesHere()
	opts := make([]string, len(here))
	for i, o := range here {
		opts[i] = o.label
	}
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_theme_mode",
			Name:     "Theme mode",
			Icon:     "mdi:theme-light-dark",
			Category: esphome.CategoryConfig,
		},
		Options: opts,
	}
	s.OnCommand = func(v string) {
		for _, o := range themeModes {
			if o.label != v {
				continue
			}
			// Auto shared from a device with a light sensor is Dark on one without.
			if o.value == config.ThemeModeAuto && !hasLightSensor() {
				o = themeModes[0]
				v = o.label
			}
			if err := config.Set().Screen().ThemeMode(o.value); err != nil {
				slog.Warn("saving the theme mode failed", "err", err)
				return
			}
			slog.Info("theme mode", "mode", v)
			s.Set(v)
			wake()
			return
		}
	}
	return s
}

// themesChanged is a change to the themes or the mode saved from the setup page: Home Assistant is told
// the mode and both themes, and the screen redrawn in the theme now in force.
func (d *Display) themesChanged() {
	if d.themeModeSel != nil {
		d.themeModeSel.Set(themeModesHere()[themeModeIndex()].label)
	}
	d.syncThemeSelects()
	d.wake()
}
