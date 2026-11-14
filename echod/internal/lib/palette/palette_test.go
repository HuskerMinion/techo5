package palette

import "testing"

func TestColorsFromSettingsAreColorsOrNothing(t *testing.T) {
	p, ok := FromHex("Custom", "#1c1511", "#E9A23B", "#e8dcc8", "#8a7d6c", "#3a2c22")
	if !ok || p.Accent != 0xe9a23b || p.Hex()[1] != "#e9a23b" {
		t.Fatalf("a good palette read as %+v, %v", p, ok)
	}
	for _, bad := range [][]string{
		{"#1c1511", "red", "#e8dcc8", "#8a7d6c", "#3a2c22"},
		{"#1c1511", "#e9a23b;}body{x:y", "#e8dcc8", "#8a7d6c", "#3a2c22"},
		{"#1c151", "#e9a23b", "#e8dcc8", "#8a7d6c", "#3a2c22"},
		{"#1c1511", "#e9a23b", "#e8dcc8", "#8a7d6c"},
	} {
		if _, ok := FromHex("Custom", bad...); ok {
			t.Errorf("FromHex(%q) was accepted", bad)
		}
	}
}

func TestLightThemesAreKnownToBeLight(t *testing.T) {
	for _, p := range Presets {
		want := p.Name == "Paper" || p.Name == "Linen"
		if p.Light() != want {
			t.Errorf("%s: Light() = %v", p.Name, p.Light())
		}
	}
	if Named("nope").Name != Presets[0].Name {
		t.Error("an unknown name is not the default")
	}
}

// A theme's dim text reads on the cards the screen draws a little off its ground (the display's
// surface(3): 18 a channel lighter on a dark theme, 12 darker on a light one), and its accent and text
// on the ground itself.
func TestPresetsReadOnTheirCards(t *testing.T) {
	shift := func(c uint32, by int) uint32 {
		ch := func(v uint32) uint32 { return uint32(min(max(int(v)+by, 0), 255)) }
		return ch(c>>16&0xff)<<16 | ch(c>>8&0xff)<<8 | ch(c&0xff)
	}
	for _, p := range append([]Preset{Round}, Presets...) {
		card := shift(p.Ground, 18)
		if p.Light() {
			card = shift(p.Ground, -12)
		}
		if c := Contrast(p.Dim, card); c < 4.5 {
			t.Errorf("%s: dim text is %.2f:1 on a card", p.Name, c)
		}
		if c := Contrast(p.Accent, p.Ground); c < 4.5 {
			t.Errorf("%s: the accent is %.2f:1 on the ground", p.Name, c)
		}
		if c := Contrast(p.Text, card); c < 7 {
			t.Errorf("%s: text is %.2f:1 on a card", p.Name, c)
		}
		// Dim stays dim: clearly weaker than the text beside it.
		if Contrast(p.Dim, p.Ground) > 0.62*Contrast(p.Text, p.Ground) {
			t.Errorf("%s: dim text is nearly as strong as the text (%.1f against %.1f)", p.Name,
				Contrast(p.Dim, p.Ground), Contrast(p.Text, p.Ground))
		}
	}
}
