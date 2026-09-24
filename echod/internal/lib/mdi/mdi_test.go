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
