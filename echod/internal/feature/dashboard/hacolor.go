//go:build !dot

package dashboard

import (
	"image/color"
	"strings"
)

// Home Assistant's named card colors: the palette its frontend defines as --<name>-color, and the
// CSS names a card may also use. A card's color is one of these, a hex or rgb value, or var(--red-color).
var haColors = map[string]color.RGBA{
	"red": {0xf4, 0x43, 0x36, 255}, "pink": {0xe9, 0x1e, 0x63, 255}, "purple": {0x92, 0x6b, 0xc7, 255},
	"deep-purple": {0x6e, 0x41, 0xab, 255}, "indigo": {0x3f, 0x51, 0xb5, 255}, "blue": {0x21, 0x96, 0xf3, 255},
	"light-blue": {0x03, 0xa9, 0xf4, 255}, "cyan": {0x00, 0xbc, 0xd4, 255}, "teal": {0x00, 0x96, 0x88, 255},
	"green": {0x4c, 0xaf, 0x50, 255}, "light-green": {0x8b, 0xc3, 0x4a, 255}, "lime": {0xcd, 0xdc, 0x39, 255},
	"yellow": {0xff, 0xeb, 0x3b, 255}, "amber": {0xff, 0xc1, 0x07, 255}, "orange": {0xff, 0x98, 0x00, 255},
	"deep-orange": {0xff, 0x6f, 0x22, 255}, "brown": {0x79, 0x55, 0x48, 255}, "light-grey": {0xbd, 0xbd, 0xbd, 255},
	"grey": {0x9e, 0x9e, 0x9e, 255}, "gray": {0x9e, 0x9e, 0x9e, 255}, "dark-grey": {0x60, 0x60, 0x60, 255},
	"dark-gray": {0x60, 0x60, 0x60, 255}, "blue-grey": {0x60, 0x7d, 0x8b, 255}, "blue-gray": {0x60, 0x7d, 0x8b, 255},
	"black": {0, 0, 0, 255}, "white": {255, 255, 255, 255}, "disabled": {0xbd, 0xbd, 0xbd, 255},
}

// CardColor reads a card's color setting: a Home Assistant name (red, amber, deep-orange), var(--red-color),
// #rgb, #rrggbb, rgb(), or a list of three numbers. It is false for no color, one it cannot read, and
// the theme's own (primary, accent), which leave the theme's colors in use.
func CardColor(v any) (color.RGBA, bool) {
	switch x := v.(type) {
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		if inner, ok := strings.CutPrefix(s, "var(--"); ok {
			s = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(inner), ")"), "-color")
		}
		if c, ok := haColors[s]; ok {
			return c, true
		}
		if strings.HasPrefix(s, "#") || strings.Contains(s, "(") || strings.Contains(s, ",") {
			if transparent(s) {
				return color.RGBA{}, false
			}
			return parseColor(s)
		}
	case []any:
		if len(x) == 3 {
			var out [3]uint8
			for i, n := range x {
				f, ok := n.(float64)
				if !ok || f < 0 || f > 255 {
					return color.RGBA{}, false
				}
				out[i] = uint8(f)
			}
			return color.RGBA{out[0], out[1], out[2], 255}, true
		}
	}
	return color.RGBA{}, false
}

// cardColorOf is the color a card or entity asks for, from color or, for Mushroom's cards, icon_color.
func cardColorOf(m raw) color.RGBA {
	for _, k := range []string{"color", "icon_color"} {
		if c, ok := CardColor(m[k]); ok {
			return c
		}
	}
	return color.RGBA{}
}

// colorsOnlyWhenActive are the domains whose tile takes its color only while it is on, as Home
// Assistant's tile card does: a sensor shows it always, a light only when lit.
var colorsOnlyWhenActive = map[string]bool{
	"light": true, "switch": true, "input_boolean": true, "fan": true, "siren": true, "automation": true,
	"cover": true, "climate": true, "media_player": true, "lock": true, "binary_sensor": true,
	"humidifier": true, "vacuum": true, "remote": true, "script": true,
}
