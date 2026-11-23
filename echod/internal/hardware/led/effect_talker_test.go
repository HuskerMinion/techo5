package led

import "testing"

// The default ring blue comes out as Amazon's pair, and white, which has no hue to turn, as the default.
func TestListeningColorsFollowTheRing(t *testing.T) {
	ring, arc := listeningColors(Palette{{R: 0, G: 71, B: 255}})
	if ring.B < 150 || ring.R > 20 || arc.G < 180 || arc.B < 200 {
		t.Errorf("blue gives ring %+v, arc %+v: want deep blue and cyan", ring, arc)
	}
	ring, arc = listeningColors(Palette{{R: 255, G: 255, B: 255}})
	if ring != listening[0] || arc != listening[1] {
		t.Errorf("white gives ring %+v, arc %+v: want the default pair", ring, arc)
	}
	ring, arc = listeningColors(Palette{{R: 255}})
	if ring.R < 150 || arc == ring {
		t.Errorf("red gives ring %+v, arc %+v", ring, arc)
	}
	if c := fromHSV(toHSV(Color{R: 12, G: 200, B: 77})); c != (Color{R: 12, G: 200, B: 77}) {
		t.Errorf("hsv round trip gives %+v", c)
	}
}
