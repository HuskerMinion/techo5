package palette

import "math"

// Derive is a whole theme from one accent: the other four colors are the accent's hue at the
// lightness each part needs, as the presets are made. On a dark theme the ground is the hue all but
// black, the rules a step lighter, the dim text a mid tone and the text near white; on a light theme
// the same the other way around. The saturation is the accent's, held down for each part, so a gray
// accent makes a gray theme and a vivid one only tints the rest. It is a starting point the custom
// colors editor offers (setup page, "Match the accent"); every color can still be changed after.
func Derive(accent uint32, light bool) Preset {
	h, s, _ := toHSL(accent)
	tint := func(most, part, l float64) uint32 { return fromHSL(h, math.Min(s*part, most), l) }
	p := Preset{Name: "Custom", Accent: accent & 0xffffff}
	if light {
		p.Ground = tint(0.45, 0.6, 0.93)
		p.Rules = tint(0.35, 0.5, 0.80)
		p.Dim = tint(0.15, 0.3, 0.42)
		p.Text = tint(0.25, 0.4, 0.12)
	} else {
		p.Ground = tint(0.35, 0.6, 0.08)
		p.Rules = tint(0.30, 0.5, 0.18)
		p.Dim = tint(0.15, 0.3, 0.50)
		p.Text = tint(0.35, 0.5, 0.88)
	}
	return p
}

// Contrast is the contrast ratio of two colors, from 1 (the same) to 21 (black on white), as the web's
// accessibility guidelines work it out.
func Contrast(a, b uint32) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// luminance is a color's relative luminance, 0 for black to 1 for white.
func luminance(v uint32) float64 {
	lin := func(c uint32) float64 {
		f := float64(c&0xff) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(v>>16) + 0.7152*lin(v>>8) + 0.0722*lin(v)
}

// toHSL is a color's hue in degrees and its saturation and lightness, 0 to 1.
func toHSL(v uint32) (h, s, l float64) {
	r, g, b := float64(v>>16&0xff)/255, float64(v>>8&0xff)/255, float64(v&0xff)/255
	hi, lo := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (hi + lo) / 2
	if hi == lo {
		return 0, 0, l
	}
	d := hi - lo
	s = d / (1 - math.Abs(2*l-1))
	switch hi {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, l
}

// fromHSL is a color from a hue in degrees and a saturation and lightness, 0 to 1.
func fromHSL(h, s, l float64) uint32 {
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
	ch := func(f float64) uint32 { return uint32(math.Round(math.Min(math.Max(f+m, 0), 1) * 255)) }
	return ch(r)<<16 | ch(g)<<8 | ch(b)
}
