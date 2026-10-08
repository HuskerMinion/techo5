//go:build !dot

package presence

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"testing"
	"time"
)

// room is a still scene with a little sensor noise, a block of it brighter where something stands at
// x (none for x < 0), and the whole picture lit by light.
func room(r *rand.Rand, x, light int) []uint8 {
	g := make([]uint8, gridW*gridH)
	for y := range gridH {
		for c := range gridW {
			v := 80 + (c*3+y*2)%40 + r.IntN(7) - 3 + light
			if x >= 0 && c >= x && c < x+5 && y >= 6 && y < 22 {
				v += 70
			}
			g[y*gridW+c] = uint8(min(max(v, 0), 255))
		}
	}
	return g
}

func TestAStillRoomIsEmpty(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var d detector
	for i := range 40 {
		if d.step(room(r, -1, 0)) {
			t.Fatalf("frame %d: noise taken for somebody", i)
		}
	}
	// Somebody standing still in a room is not movement either, once they have stopped.
	for i := range 10 {
		if d.step(room(r, 10, 0)) && i > 3 {
			t.Fatalf("frame %d: a still figure taken for movement", i)
		}
	}
}

func TestSomebodyWalkingIsSeen(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	var d detector
	d.step(room(r, -1, 0))
	seen := false
	for i := range 6 {
		if d.step(room(r, 2+i*4, 0)) {
			seen = true
			if i < 1 {
				t.Errorf("seen at the first moving frame; two are needed")
			}
		}
	}
	if !seen {
		t.Fatal("somebody walking across was not seen")
	}
}

// A light switched on changes every cell at once: that is not somebody, and the next frame is compared
// with the room as newly lit.
func TestALightIsNotSomebody(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	var d detector
	d.step(room(r, -1, 0))
	for i := range 6 {
		light := 0
		if i >= 1 {
			light = 60
		}
		if d.step(room(r, -1, light)) {
			t.Fatalf("frame %d: the light was taken for somebody", i)
		}
	}
	// A slow change of the whole picture (the evening coming on) is not movement either.
	for i := range 20 {
		if d.step(room(r, -1, 60-i*2)) {
			t.Fatalf("frame %d: a slow fade was taken for somebody", i)
		}
	}
}

// What fooled the first version on a Show 8: the light switched off, then the exposure climbing back
// over a few frames, unevenly (the dark parts of the picture come up by more than the bright ones).
func TestTheExposureSettlingIsNotSomebody(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	var d detector
	for range 3 {
		d.step(room(r, -1, 0))
	}
	settle := func(g []uint8, k float64) []uint8 {
		out := make([]uint8, len(g))
		for i, v := range g {
			f := float64(v) / 255
			// A curve, not a scale: the camera's gamma and its own exposure steps.
			f = math.Pow(f, 1/(0.4+0.6*k)) * (0.3 + 0.7*k)
			out[i] = uint8(min(max(f*255, 0), 255))
		}
		return out
	}
	steps := []float64{0.15, 0.3, 0.45, 0.6, 0.7, 0.8, 0.9, 0.95, 1, 1, 1, 1}
	for i, k := range steps {
		if d.step(settle(room(r, -1, -50), k)) {
			t.Fatalf("frame %d of the light going off: taken for somebody", i)
		}
	}
	for i, k := range steps {
		if d.step(settle(room(r, -1, 0), 1.6-0.6*k)) {
			t.Fatalf("frame %d of the light coming on: taken for somebody", i)
		}
	}
	// And somebody walking in after the light has settled is still seen.
	seen := false
	for i := range 6 {
		seen = seen || d.step(room(r, 2+i*4, 0))
	}
	if !seen {
		t.Fatal("somebody walking in after the light settled was not seen")
	}
}

// A recording from a device (a file of 8-byte times and 32x24 grids, as the test build's recorder
// wrote them), replayed when PRESENCE_GRIDS names one. Recordings stay off the repository: they are a
// picture of somebody's room. Prints when somebody would have been seen.
func TestReplayARecording(t *testing.T) {
	path := os.Getenv("PRESENCE_GRIDS")
	if path == "" {
		t.Skip("PRESENCE_GRIDS not set")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d detector
	n := gridW * gridH
	was := false
	for i := 0; i+8+n <= len(b); i += 8 + n {
		at := time.Unix(0, int64(binary.LittleEndian.Uint64(b[i:])))
		now := d.step(b[i+8 : i+8+n])
		if now != was {
			t.Logf("%s somebody near: %v", at.Format("15:04:05.00"), now)
			was = now
		}
	}
}
