//go:build !dot

package display

import "image/color"

// Themes: five colors make the whole screen — the ground, the accent, the text, a dim text and
// the rules and boxes. The palette lives in package variables the renderer reads on every frame,
// so switching is a matter of assigning them; the choice is saved with the screen settings. A
// preset is picked by name; a color changed in the custom colors editor makes the theme "Custom", saved as
// its five colors.
type theme struct {
	name   string
	colors [roles]color.RGBA
}

// The five roles, in the order the custom colors editor lists them.
const (
	roleGround = iota
	roleAccent
	roleText
	roleDim
	roleRules
	roles
)

var roleNames = [roles]string{"Ground", "Accent", "Text", "Dim text", "Rules"}

func rgb(v uint32) color.RGBA { return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff} }

func preset(name string, ground, accent, text, dim, rules uint32) theme {
	return theme{name, [roles]color.RGBA{rgb(ground), rgb(accent), rgb(text), rgb(dim), rgb(rules)}}
}
