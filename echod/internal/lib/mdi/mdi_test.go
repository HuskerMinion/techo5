package mdi

import (
	"testing"

	"golang.org/x/image/font/sfnt"
)

func TestIconsAreInTheFont(t *testing.T) {
	f, err := sfnt.Parse(Font)
	if err != nil {
		t.Fatal(err)
	}
	var b sfnt.Buffer
	for _, name := range []string{"mdi:lightbulb", "lightbulb-outline", "thermometer", "garage", "power"} {
		r, ok := Rune(name)
		if !ok {
			t.Fatalf("%s: no such icon", name)
		}
		if g, err := f.GlyphIndex(&b, r); err != nil || g == 0 {
			t.Errorf("%s (%U): not in the font", name, r)
		}
	}
	if _, ok := Rune("mdi:no-such-icon-at-all"); ok {
		t.Error("a made-up icon was found")
	}
}

func TestNameIsRuneBackwards(t *testing.T) {
	if n, ok := Name(0xF0335); !ok || n != "lightbulb" {
		t.Errorf("Name(F0335) = %q, %v; want lightbulb", n, ok)
	}
	if _, ok := Name('a'); ok {
		t.Error("a letter was named as an icon")
	}
}

// An icon named by the set's old name, as Home Assistant still writes some, is the same icon (#760).
func TestHassPrefixIsTheSameSet(t *testing.T) {
	a, ok := Rune("hass:flash")
	b, _ := Rune("mdi:flash")
	if !ok || a != b {
		t.Fatalf("hass:flash = %U, %v; mdi:flash = %U", a, ok, b)
	}
}
