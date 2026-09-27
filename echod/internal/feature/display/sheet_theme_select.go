//go:build !dot && !spot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// themeSelect is the theme in Home Assistant: the presets by name, and Custom for the palette saved
// on the device, which is chosen and edited only there. The screen's own Theme row sets the same
// setting, and the frame keeps the select showing whatever is in force (syncThemeSelect).
func themeSelect(wake func()) *esphome.Select {
	opts := make([]string, 0, len(themes)+1)
	for _, t := range themes {
		opts = append(opts, t.name)
	}
	opts = append(opts, customName)
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_theme",
			Name:     "Theme",
			Icon:     "mdi:palette",
			Category: esphome.CategoryConfig,
		},
		Options: opts,
	}
	s.OnCommand = func(v string) {
		if v == customName {
			if _, ok := savedCustom(); !ok {
				slog.Info("theme: no custom palette saved on the device yet")
				s.Set(current().name)
				return
			}
		} else if themes[themeIndex(v)].name != v {
			return
		}
		if err := config.Set().Screen().Theme(v); err != nil {
			slog.Warn("saving the theme failed", "err", err)
			return
		}
		s.Set(v)
		wake()
	}
	return s
}

// syncThemeSelect puts the theme in force on the select when it changed on the screen.
func (d *Display) syncThemeSelect(name string) {
	if d.themeSel == nil || name == d.themeShown {
		return
	}
	d.themeShown = name
	d.themeSel.Set(name)
}
