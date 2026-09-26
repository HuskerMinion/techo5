//go:build !dot

package home

import (
	"testing"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
)

// The towns on a picture are the ones inside it, largest first: around New York, the city itself.
func TestPlacesInView(t *testing.T) {
	x, y := worldPixel(40.71, -74.0, mapZoom)
	got := placesIn(int(x)-480, int(y)-240, 960, 480)
	if len(got) == 0 || got[0].Name != "New York City" {
		t.Fatalf("largest town around New York: %v", got[:min(len(got), 3)])
	}
	for i, p := range got {
		if p.At.X < 0 || p.At.Y < 0 || p.At.X >= 960 || p.At.Y >= 480 {
			t.Errorf("%s at %v is off the picture", p.Name, p.At)
		}
		if i > 0 && p.Pop > got[i-1].Pop {
			t.Errorf("%s (%d) after %s (%d): not largest first", p.Name, p.Pop, got[i-1].Name, got[i-1].Pop)
		}
	}
	if len(got) > maxPlaces {
		t.Errorf("%d towns offered, want at most %d", len(got), maxPlaces)
	}
}

// Every town's name can be drawn in the screen's font: a letter it has no glyph for would show as an
// empty box (tools/places/make_places.py gives those names in their Latin spelling).
func TestEveryPlaceNameHasGlyphs(t *testing.T) {
	f, err := sfnt.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	var buf sfnt.Buffer
	bad := 0
	for _, p := range loadPlaces() {
		for _, r := range p.name {
			if g, err := f.GlyphIndex(&buf, r); err != nil || g == 0 {
				if bad++; bad <= 10 {
					t.Errorf("%q: no glyph for %q (U+%04X)", p.name, r, r)
				}
				break
			}
		}
	}
	if bad > 10 {
		t.Errorf("and %d more", bad-10)
	}
}
