package palette

import (
	"fmt"
	"math"
	"testing"
)

// accents are colors somebody might pick: grays, vivid hues, very dark and very light ones.
var accents = []uint32{
	0x000000, 0x404040, 0x808080, 0xc0c0c0, 0xffffff,
	0xff0000, 0xffff00, 0x00ff00, 0x00ffff, 0x0000ff, 0xff00ff,
	0xe9a23b, 0x5cb8ff, 0x2ed9b8, 0xf05a3c, 0xb69cff, 0xb85c1e, 0x2c6e9e,
	0x100008, 0x001010, 0x200000, 0xfff8f0, 0xf0fff0, 0xe0e0ff,
}

func TestDerivedTextReadsOnItsGround(t *testing.T) {
	for _, a := range accents {
		for _, light := range []bool{false, true} {
			p := Derive(a, light)
			name := fmt.Sprintf("accent #%06x, light %v", a, light)
			if c := Contrast(p.Text, p.Ground); c < 7 {
				t.Errorf("%s: text on ground is %.1f:1, under 7:1 (%v)", name, c, p.Hex())
			}
			if c := Contrast(p.Dim, p.Ground); c < 3 {
				t.Errorf("%s: dim text on ground is %.1f:1, under 3:1 (%v)", name, c, p.Hex())
			}
			if p.Light() != light {
				t.Errorf("%s: the ground %06x is not the side asked for", name, p.Ground)
			}
			// The accent is the one given, unless a light ground could not show it.
			if p.Accent != a && !(light && Contrast(a, p.Ground) < readable) {
				t.Errorf("%s: the accent became %06x", name, p.Accent)
			}
		}
	}
}

func TestDerivedRulesSitJustOffTheGround(t *testing.T) {
	for _, a := range accents {
		for _, light := range []bool{false, true} {
			p := Derive(a, light)
			gl, rl := luminance(p.Ground), luminance(p.Rules)
			if light && rl >= gl || !light && rl <= gl {
				t.Errorf("accent #%06x, light %v: rules %06x are not a step off the ground %06x", a, light, p.Rules, p.Ground)
			}
			if c := Contrast(p.Rules, p.Ground); c > 2.5 {
				t.Errorf("accent #%06x, light %v: rules stand out from the ground at %.1f:1", a, light, c)
			}
		}
	}
}

func TestDerivedThemeKeepsTheHue(t *testing.T) {
	p := Derive(0xf05a3c, false) // Ember's accent: a red-orange
	if r, g, b := p.Ground>>16&0xff, p.Ground>>8&0xff, p.Ground&0xff; !(r > g && r > b) {
		t.Errorf("a red accent's ground %06x is not reddish", p.Ground)
	}
	gray := Derive(0x808080, true)
	for _, v := range []uint32{gray.Ground, gray.Text, gray.Dim, gray.Rules} {
		if r, g, b := v>>16&0xff, v>>8&0xff, v&0xff; r != g || g != b {
			t.Errorf("a gray accent made %06x, which is not gray", v)
		}
	}
	hx := Derive(0x5cb8ff, false).Hex()
	if _, ok := FromHex("Custom", hx[:]...); !ok {
		t.Error("a derived theme does not read back as five colors")
	}
}

func TestHSLRoundTrips(t *testing.T) {
	for _, v := range append(accents, 0x1c1511, 0xe8dcc8, 0x3a2c22) {
		h, s, l := toHSL(v)
		if back := fromHSL(h, s, l); back != v {
			t.Errorf("#%06x came back as #%06x", v, back)
		}
	}
}

// Whatever the accent, the theme made from it can be read: the dim text 4.5:1 on a card and still
// plainly dimmer than the text, the text 7:1 there, and on a light theme the accent 4.5:1 on the
// ground, darker if it had to be but the hue it was. Every hue, gray to vivid, dark to pale, both ways.
func TestDerivedThemesReadWhateverTheAccent(t *testing.T) {
	worst := map[string]float64{}
	note := func(what string, light bool, c float64) {
		k := fmt.Sprintf("%s, light %v", what, light)
		if w, ok := worst[k]; !ok || c < w {
			worst[k] = c
		}
	}
	for hue := 0.0; hue < 360; hue += 5 {
		for _, sat := range []float64{0, 0.2, 0.5, 0.8, 1} {
			for _, lum := range []float64{0.05, 0.2, 0.35, 0.5, 0.65, 0.8, 0.95} {
				a := fromHSL(hue, sat, lum)
				for _, light := range []bool{false, true} {
					p := Derive(a, light)
					name := fmt.Sprintf("accent #%06x, light %v (%v)", a, light, p.Hex())
					dim, text := Contrast(p.Dim, p.Card()), Contrast(p.Text, p.Card())
					note("dim text on a card", light, dim)
					note("text on a card", light, text)
					if dim < 4.5 {
						t.Errorf("%s: dim text is %.2f:1 on a card", name, dim)
					}
					if text < 7 {
						t.Errorf("%s: text is %.2f:1 on a card", name, text)
					}
					if d, x := Contrast(p.Dim, p.Ground), Contrast(p.Text, p.Ground); d > 0.62*x {
						t.Errorf("%s: dim text is nearly as strong as the text (%.1f against %.1f)", name, d, x)
					}
					if !light {
						if p.Accent != a {
							t.Errorf("%s: a dark theme's accent became %06x", name, p.Accent)
						}
						continue
					}
					on := Contrast(p.Accent, p.Ground)
					note("accent on the ground", light, on)
					if on < 4.5 {
						t.Errorf("%s: the accent is %.2f:1 on the ground", name, on)
					}
					if was := Contrast(a, p.Ground); was >= 4.5 && p.Accent != a {
						t.Errorf("%s: the accent read at %.2f:1 and still became %06x", name, was, p.Accent)
					}
					// The hue is kept, as near as 8 bits a channel hold it in a dark color.
					h0, s0, _ := toHSL(a)
					h1, s1, l1 := toHSL(p.Accent)
					if turn := math.Abs(math.Mod(h1-h0+540, 360) - 180); s0 > 0.1 && s1 > 0.1 && l1 > 0.1 && turn > 4 {
						t.Errorf("%s: the accent's hue went from %.0f to %.0f", name, h0, h1)
					}
				}
			}
		}
	}
	for k, c := range worst {
		t.Logf("at worst, %s: %.2f:1", k, c)
	}
}
