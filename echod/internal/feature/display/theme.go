//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/palette"
)

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

// themes are the presets, from lib/palette, where the setup page reads them too.
var themes = func() []theme {
	out := make([]theme, len(palette.Presets))
	for i, p := range palette.Presets {
		out[i] = preset(p.Name, p.Ground, p.Accent, p.Text, p.Dim, p.Rules)
	}
	return out
}()

const customName = "Custom"

// themeIndex finds a preset by name; unknown names (and Custom) are the first.
func themeIndex(name string) int {
	for i, t := range themes {
		if t.name == name {
			return i
		}
	}
	return 0
}

// current is the palette in force, from the config.
func current() theme {
	sc := config.Get().Screen
	if sc.Theme == customName {
		t := theme{name: customName}
		for i, hex := range [roles]string{sc.Palette.Ground, sc.Palette.Accent, sc.Palette.Text, sc.Palette.Dim, sc.Palette.Rules} {
			c, err := parseHex(hex)
			if err != nil {
				return themes[0]
			}
			t.colors[i] = c
		}
		return t
	}
	return themes[themeIndex(sc.Theme)]
}

// savedCustom is the custom palette saved, whether or not it is in force.
func savedCustom() (theme, bool) {
	p := config.Get().Screen.Palette
	t := theme{name: customName}
	for i, hex := range [roles]string{p.Ground, p.Accent, p.Text, p.Dim, p.Rules} {
		c, err := parseHex(hex)
		if err != nil {
			return theme{}, false
		}
		t.colors[i] = c
	}
	return t, true
}

// applyTheme sets the palette the renderer draws with. Called from the display's goroutine only.
func applyTheme(t theme) {
	walnut, amber, cream, dim, ember = t.colors[roleGround], t.colors[roleAccent], t.colors[roleText], t.colors[roleDim], t.colors[roleRules]
}

// setRole changes one color of the palette in force and saves the result as Custom.
func setRole(role int, c color.RGBA) {
	t := current()
	t.colors[role] = c
	p := config.Palette{Ground: hex(t.colors[0]), Accent: hex(t.colors[1]), Text: hex(t.colors[2]), Dim: hex(t.colors[3]), Rules: hex(t.colors[4])}
	if err := config.Set().Screen().Custom(p); err != nil {
		slog.Warn("saving the custom theme failed", "err", err)
	}
	slog.Info("theme", "role", roleNames[role], "color", hex(c))
}

func hex(c color.RGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func parseHex(s string) (color.RGBA, error) {
	var r, g, b uint8
	if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return color.RGBA{}, err
	}
	return color.RGBA{r, g, b, 0xff}, nil
}

// hsl makes a color from hue (degrees), saturation and lightness (0..1).
func hsl(h, s, l float64) color.RGBA {
	c := (1 - math.Abs(2*l-1)) * s
	hp := math.Mod(h, 360) / 60
	x := c * (1 - math.Abs(math.Mod(hp, 2)-1))
	var r, g, b float64
	switch {
	case hp < 1:
		r, g, b = c, x, 0
	case hp < 2:
		r, g, b = x, c, 0
	case hp < 3:
		r, g, b = 0, c, x
	case hp < 4:
		r, g, b = 0, x, c
	case hp < 5:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	m := l - c/2
	return color.RGBA{uint8((r + m) * 255), uint8((g + m) * 255), uint8((b + m) * 255), 0xff}
}

// swatchCount is how many swatches a role's strip has: two neutrals and twelve hues.
const swatchCount = 14

// swatch is the i-th choice for a role: the strip is lit for the job the role does — grounds
// dark, accents vivid, text pale, dim text muted, rules a shade above a ground.
func swatch(role, i int) color.RGBA {
	var s, l float64
	switch role {
	case roleGround:
		s, l = 0.35, 0.13
	case roleAccent:
		s, l = 0.85, 0.60
	case roleText:
		s, l = 0.35, 0.88
	case roleDim:
		s, l = 0.15, 0.50
	default:
		s, l = 0.30, 0.24
	}
	switch i {
	case 0: // a neutral at the role's lightness
		return hsl(0, 0, l)
	case 1: // a light neutral, for light themes and text
		if role == roleGround || role == roleRules {
			return hsl(30, 0.25, 1-l*0.7)
		}
		return hsl(0, 0, 0.2+l*0.7)
	}
	return hsl(float64(i-2)*30, s, l)
}

// bevel draws a box with depth: a fill, a lit top and left edge, a shaded bottom and right edge,
// and a soft shadow under it. raised false sinks the box instead.
func (r *renderer) bevel(rect image.Rectangle, fill color.RGBA, raised bool) {
	light, shadow := shift(fill, 28), shift(fill, -22)
	if !dark() {
		light, shadow = shift(fill, 18), shift(fill, -30)
	}
	if !raised {
		light, shadow = shadow, light
	}
	if raised {
		sh := image.Rect(rect.Min.X+3, rect.Max.Y, rect.Max.X+3, rect.Max.Y+3)
		draw.Draw(r.dst, sh, image.NewUniform(shade), image.Point{}, draw.Over)
		sh = image.Rect(rect.Max.X, rect.Min.Y+3, rect.Max.X+3, rect.Max.Y)
		draw.Draw(r.dst, sh, image.NewUniform(shade), image.Point{}, draw.Over)
	}
	draw.Draw(r.dst, rect, image.NewUniform(fill), image.Point{}, draw.Src)
	draw.Draw(r.dst, image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+2), image.NewUniform(light), image.Point{}, draw.Src)
	draw.Draw(r.dst, image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+2, rect.Max.Y), image.NewUniform(light), image.Point{}, draw.Src)
	draw.Draw(r.dst, image.Rect(rect.Min.X, rect.Max.Y-2, rect.Max.X, rect.Max.Y), image.NewUniform(shadow), image.Point{}, draw.Src)
	draw.Draw(r.dst, image.Rect(rect.Max.X-2, rect.Min.Y, rect.Max.X, rect.Max.Y), image.NewUniform(shadow), image.Point{}, draw.Src)
}

// swatchStrip draws a ctlSwatches row's colors for one role of the theme, the one in force ringed in
// the text color; a tap on one picks it.
func (r *paint) swatchStrip(row settingRow, right, cy, top int) {
	const d, gap = 28, 6
	x0 := right - swatchCount*d - (swatchCount-1)*gap
	in := current().colors[row.role]
	for i := range swatchCount {
		c := swatch(row.role, i)
		cx, cyf := float64(x0+i*(d+gap)+d/2), float64(cy)
		r.aaDisc(cx, cyf+1.5, d/2, lerp(c, color.RGBA{0, 0, 0, 255}, 0.5)) // a little depth under it
		r.aaDisc(cx, cyf, d/2, c)
		r.aaRing(cx, cyf, d/2-0.5, 1, 0, 2*math.Pi, lerp(c, color.RGBA{255, 255, 255, 255}, 0.25))
		if c == in {
			r.aaRing(cx, cyf, d/2+4, 2.4, 0, 2*math.Pi, cream)
		}
		if row.id != "" {
			x := x0 + i*(d+gap)
			r.addZone(zone{r: image.Rect(x-gap/2, top, x+d+gap/2, top+r.rowH()), kind: zoneRow, id: row.id, part: partDay, opt: i})
		}
	}
}
