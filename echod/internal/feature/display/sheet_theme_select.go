//go:build !dot && !spot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// themeSelect is a theme in Home Assistant: Theme for the dark mode's (light false), as it always was,
// and Light theme for the light mode's (theme_mode.go); the presets by name, and Custom for that mode's
// palette saved on the device. Each is the one setting whatever the mode and the room: neither changes
// when the mode does or Auto turns, so a select a linked device copies (feature/share) is always the
// same mode's. The screen's own Theme row sets the mode it is wearing, and the frame keeps both selects
// showing what is saved (syncThemeSelects).
func themeSelect(light bool, wake func()) *esphome.Select {
	opts := make([]string, 0, len(themes)+1)
	for _, t := range themes {
		opts = append(opts, t.name)
	}
	opts = append(opts, customName)
	id, name, icon := "screen_theme", "Theme", "mdi:palette"
	if light {
		id, name, icon = "screen_light_theme", "Light theme", "mdi:palette-outline"
	}
	s := &esphome.Select{
		Base:    esphome.Base{ObjectID: id, Name: name, Icon: icon, Category: esphome.CategoryConfig},
		Options: opts,
	}
	s.OnCommand = func(v string) {
		if v == customName {
			if _, ok := savedCustom(light); !ok {
				slog.Info("theme: no custom palette saved on the device yet", "light", light)
				s.Set(modeThemeName(light))
				return
			}
		} else if themes[themeIndex(v)].name != v {
			return
		}
		if err := config.Set().Screen().ThemeFor(light, v); err != nil {
			slog.Warn("saving the theme failed", "err", err)
			return
		}
		s.Set(v)
		wake()
	}
	return s
}

// modeThemeName is the light mode's theme as a select shows it, or the dark mode's: the preset's name, or
// Custom while its palette is whole; whatever else is saved is the theme drawn in its place (current).
func modeThemeName(light bool) string {
	name := config.Get().Screen.ThemeFor(light)
	if name == customName {
		if _, ok := savedCustom(light); ok {
			return customName
		}
		return themes[0].name
	}
	return themes[themeIndex(name)].name
}

// syncThemeSelects puts each mode's theme on its select when it changed on the screen or the setup page.
func (d *Display) syncThemeSelects() {
	d.themeMu.Lock()
	defer d.themeMu.Unlock()
	for i, sel := range [2]*esphome.Select{d.themeSel, d.lightThemeSel} {
		if sel == nil {
			continue
		}
		if name := modeThemeName(i == 1); name != d.themeShown[i] {
			d.themeShown[i] = name
			sel.Set(name)
		}
	}
}
