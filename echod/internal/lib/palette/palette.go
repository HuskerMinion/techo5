// Package palette is the screen themes as data: five colors each, by name. The Show's screen draws
// with them (feature/display/theme.go) and the setup page wears the same ones, so a device's page
// looks like its screen. It imports nothing, so anything may use it.
package palette

import "fmt"

// Preset is one theme: the ground, the accent, the text, a dim text and the rules and boxes, as
// 0xRRGGBB.
type Preset struct {
	Name                             string
	Ground, Accent, Text, Dim, Rules uint32
}

// Presets are the themes offered, in the order the screen lists them; the first is the default.
var Presets = []Preset{
	{"Walnut", 0x1c1511, 0xe9a23b, 0xe8dcc8, 0x8a7d6c, 0x3a2c22},
	{"Slate", 0x141920, 0x5cb8ff, 0xe4eaf0, 0x7c8896, 0x27303b},
	{"Midnight", 0x080a10, 0x2ed9b8, 0xdde6e8, 0x6c7a80, 0x181e2a},
	{"Forest", 0x101a14, 0xd8b44a, 0xe6ecdc, 0x7d8c78, 0x223428},
	{"Plum", 0x1a101c, 0xf07ca8, 0xf0e4ec, 0x8c7488, 0x36243c},
	{"Ocean", 0x0a1622, 0x36c6e0, 0xdcecf4, 0x6e8896, 0x163040},
	{"Ember", 0x180c0a, 0xf05a3c, 0xf2e2da, 0x8e7068, 0x3a1c16},
	{"Mint", 0x0e1a18, 0x6ee7b7, 0xe2f2ec, 0x709088, 0x1c342e},
	{"Lavender", 0x14121e, 0xb69cff, 0xeae6f4, 0x8078a0, 0x2a2640},
	{"Graphite", 0x161616, 0xffffff, 0xe0e0e0, 0x8a8a8a, 0x303030},
	{"Cherry", 0x1c0a10, 0xff3b6b, 0xf4e0e6, 0x907080, 0x3c1824},
	{"Paper", 0xf2eadc, 0xb85c1e, 0x2a221c, 0x7a6e62, 0xd8ccb8},
	{"Linen", 0xf6f1e8, 0x2c6e9e, 0x1e2630, 0x6f7a86, 0xd9d1c4},
}

// Round is the Spot's palette, which is not chosen: the round screen has one look.
var Round = Preset{"Round", 0x0a0d12, 0xf05a3c, 0xecf0f4, 0x808a96, 0x252c36}

// Hex is the five colors as "#rrggbb", ground first.
func (p Preset) Hex() [5]string {
	var out [5]string
	for i, v := range []uint32{p.Ground, p.Accent, p.Text, p.Dim, p.Rules} {
		out[i] = fmt.Sprintf("#%06x", v&0xffffff)
	}
	return out
}

// Light is whether the ground is pale, so what is drawn on it should be dark.
func (p Preset) Light() bool {
	r, g, b := p.Ground>>16&0xff, p.Ground>>8&0xff, p.Ground&0xff
	return r*299+g*587+b*114 > 128*1000
}

// FromHex makes a preset from five "#rrggbb" colors, and reports false unless every one is exactly
// that: they come from saved settings and go into a style sheet, so nothing else gets through.
func FromHex(name string, colors ...string) (Preset, bool) {
	if len(colors) != 5 {
		return Preset{}, false
	}
	var v [5]uint32
	for i, s := range colors {
		if len(s) != 7 || s[0] != '#' {
			return Preset{}, false
		}
		for _, c := range s[1:] {
			var d uint32
			switch {
			case c >= '0' && c <= '9':
				d = uint32(c - '0')
			case c >= 'a' && c <= 'f':
				d = uint32(c-'a') + 10
			case c >= 'A' && c <= 'F':
				d = uint32(c-'A') + 10
			default:
				return Preset{}, false
			}
			v[i] = v[i]<<4 | d
		}
	}
	return Preset{name, v[0], v[1], v[2], v[3], v[4]}, true
}

// Named is the preset of that name, or the first for an unknown one.
func Named(name string) Preset {
	for _, p := range Presets {
		if p.Name == name {
			return p
		}
	}
	return Presets[0]
}
