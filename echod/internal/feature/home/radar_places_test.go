//go:build !dot

package home

import "testing"

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
