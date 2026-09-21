//go:build spot

package camera

import "testing"

// rggb makes a frame whose red, green and blue sites carry the given levels.
func rggb(r, g, b byte) []byte {
	raw := make([]byte, frameBytes)
	for y := 0; y < sensorH; y++ {
		for x := 0; x < sensorW; x++ {
			v := g
			switch {
			case y%2 == 0 && x%2 == 0:
				v = r
			case y%2 == 1 && x%2 == 1:
				v = b
			}
			raw[y*sensorW+x] = v
		}
	}
	return raw
}

// A tinted flat field comes out grey: the white balance evens the channels, and the demosaic fills
// every pixel, edges included.
func TestConvertBalancesAFlatField(t *testing.T) {
	img, tone := convert(rggb(40, 80, 60))
	if tone.gainR < 1.9 || tone.gainR > 2.1 || tone.gainB < 1.2 || tone.gainB > 1.45 {
		t.Fatalf("gains R %.2f B %.2f, want about 2 and 1.33", tone.gainR, tone.gainB)
	}
	for _, p := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {320, 240}, {639, 479}, {638, 479}} {
		j := (p[1]*sensorW + p[0]) * 4
		r, g, b := int(img.Pix[j]), int(img.Pix[j+1]), int(img.Pix[j+2])
		if abs(r-g) > 6 || abs(b-g) > 6 {
			t.Errorf("pixel %v is %d,%d,%d, not grey", p, r, g, b)
		}
	}
}

// Levels are stretched: the brightest cells reach white, and a dark frame is not stretched into noise.
func TestLevels(t *testing.T) {
	raw := rggb(10, 10, 10)
	for i := 0; i < sensorW*40; i++ {
		raw[i] = 200
	}
	tn := stats(raw)
	if tn.white < 150 {
		t.Errorf("white point %d, want the bright rows near it", tn.white)
	}
	if dark := stats(rggb(2, 2, 2)); dark.white-dark.black < 24 {
		t.Errorf("a black frame spans %d..%d, want at least 24 levels", dark.black, dark.white)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
