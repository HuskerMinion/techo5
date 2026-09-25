//go:build !dot && !spot

package camera

import (
	"math"
	"testing"
)

// pack builds a packed 10-bit frame from a picture, the inverse of unpackLine.
func pack(value func(x, y int) uint16) []byte {
	raw := make([]byte, frameBytes)
	for y := 0; y < sensorH; y++ {
		line := raw[y*bytesPerLine:]
		for g := 0; g < sensorW/4; g++ {
			v := [4]uint16{value(4*g, y), value(4*g+1, y), value(4*g+2, y), value(4*g+3, y)}
			i := g * 5
			line[i] = byte(v[0])
			line[i+1] = byte(v[0]>>8&3) | byte(v[1]<<2)
			line[i+2] = byte(v[1]>>6&0xF) | byte(v[2]<<4)
			line[i+3] = byte(v[2]>>4&0x3F) | byte(v[3]<<6)
			line[i+4] = byte(v[3] >> 2)
		}
	}
	return raw
}

func TestPackRoundTrips(t *testing.T) {
	raw := pack(func(x, y int) uint16 { return uint16((x*7 + y*13) % 1024) })
	px := make([]uint16, sensorW)
	for _, y := range []int{0, 1, sensorH / 2, sensorH - 1} {
		unpackLine(raw[y*bytesPerLine:(y+1)*bytesPerLine], px)
		for x := 0; x < sensorW; x++ {
			if want := uint16((x*7 + y*13) % 1024); px[x] != want {
				t.Fatalf("pixel %d,%d = %d, want %d", x, y, px[x], want)
			}
		}
	}
}

// A flat scene meters at its level, whatever the weights.
func TestMeterFlatScene(t *testing.T) {
	if level := meter(pack(func(x, y int) uint16 { return 290 })); math.Abs(level-290) > 1 {
		t.Errorf("flat 290: level %.1f", level)
	}
}

// backlit is a bright window across the top half, a dim face in the middle, a room around it,
// in eighths and twelfths of whichever sensor this build has.
func backlit(x, y int) uint16 {
	fx, fy := x*8/sensorW, y*12/sensorH
	switch {
	case fx >= 3 && fx < 5 && fy >= 4 && fy < 9:
		return 120 // the face
	case fy < 6:
		return 1010 // the window
	default:
		return 200
	}
}

func TestMeterFavoursTheMiddleOverAWindow(t *testing.T) {
	raw := pack(backlit)

	// What the plain mean the loop used to take would say.
	var sum, n float64
	for y := 0; y < sensorH; y += 2 {
		for x := 0; x < sensorW; x += 8 {
			sum += float64(backlit(x, y))
			n++
		}
	}
	plain := sum / n

	level := meter(raw)
	t.Logf("plain mean %.0f, metered %.0f", plain, level)
	if level > plain*0.6 {
		t.Errorf("metered %.0f, want well under the plain mean %.0f", level, plain)
	}
	// Under the target, so the loop brightens toward the face instead of darkening for the window.
	if level >= aeTarget {
		t.Errorf("metered %.0f is at or over the target %d: the face would stay dark", level, aeTarget)
	}
}

// A frame bright everywhere still meters bright, so the loop darkens it.
func TestMeterClippedEverywhere(t *testing.T) {
	if level := meter(pack(func(x, y int) uint16 { return 1015 })); level < aeTarget*2 {
		t.Errorf("an all-white frame metered %.0f", level)
	}
}

func TestBlackPoint(t *testing.T) {
	for _, tc := range []struct{ low, white, want int }{
		{43, 2046, 43},  // a real room's floor
		{300, 2046, 64}, // nothing dark in the frame: capped
		{40, 100, 25},   // a dim frame keeps most of its range
		{-1, 2046, 0},   // never negative
	} {
		if got := blackFor(tc.low, tc.white); got != tc.want {
			t.Errorf("blackFor(%d, %d) = %d, want %d", tc.low, tc.white, got, tc.want)
		}
	}
}

func TestGammaSteepensOnlyForHighRange(t *testing.T) {
	if g := gammaFor(600, 1400); g != gammaNormal {
		t.Errorf("an ordinary frame got gamma %.3f", g)
	}
	steep := gammaFor(240, 2000)
	if steep >= gammaNormal || steep < 1/2.7-1e-9 {
		t.Errorf("a backlit frame got gamma %.3f, want between 1/2.7 and 1/1.8", steep)
	}
	if g := gammaFor(0, 2047); g < 1/2.7-1e-9 {
		t.Errorf("an extreme frame got %.3f, past the limit", g)
	}
}

// convert settles on the steeper curve for the backlit frame, and the face comes out brighter than it
// would with the old fixed one.
func TestConvertLiftsABacklitFace(t *testing.T) {
	img, tn := convert(pack(backlit))
	if tn.gamma >= gammaNormal {
		t.Fatalf("gamma %.3f, want steeper than normal for a backlit frame", tn.gamma)
	}
	face := img.RGBAAt(Width/2, Height*8/15).G // the middle of the face
	old := tone{gainR: tn.gainR, gainB: tn.gainB, white: tn.white, gamma: gammaNormal}
	oldFace := old.tables()[1][240]
	t.Logf("face green %d, with the fixed curve %d; gamma %.3f white %d gains %.2f %.2f", face, oldFace, tn.gamma, tn.white, tn.gainR, tn.gainB)
	if face <= oldFace {
		t.Errorf("face %d is not brighter than with the fixed curve (%d)", face, oldFace)
	}
}
