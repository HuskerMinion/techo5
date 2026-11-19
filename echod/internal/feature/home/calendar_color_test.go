package home

import (
	"image/color"
	"testing"
)

// A calendar's color is kept as Home Assistant writes it, "#rrggbb", and anything else is no color.
func TestHexColor(t *testing.T) {
	if c, ok := hexColor("#16a765"); !ok || c != (color.RGBA{0x16, 0xa7, 0x65, 255}) {
		t.Errorf("#16a765 = %v %v", c, ok)
	}
	for _, bad := range []string{"", "#16a76", "16a7650", "#16a76g", "#+6a765", "red"} {
		if _, ok := hexColor(bad); ok {
			t.Errorf("%q was taken as a color", bad)
		}
	}
}
