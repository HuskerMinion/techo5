//go:build !dot && !spot

package setup

import (
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/palette"
)

// pageColors is the page in the screen's own theme, so the two look like one device: the preset in
// force, or the five colors of a Custom one when every one of them is a color. In force is the theme of
// the mode the screen wears, dark or light.
func pageColors() palette.Preset {
	sc := config.Get().Screen
	light := false
	if s := screen.Load(); s != nil && s.Themes != nil && s.Themes.LightNow != nil {
		light = s.Themes.LightNow()
	}
	if name := sc.ThemeFor(light); name != "Custom" {
		return palette.Named(name)
	}
	p := sc.PaletteFor(light)
	if custom, ok := palette.FromHex("Custom", p.Ground, p.Accent, p.Text, p.Dim, p.Rules); ok {
		return custom
	}
	return palette.Presets[0]
}
