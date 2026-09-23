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
