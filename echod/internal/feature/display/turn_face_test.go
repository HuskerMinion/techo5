//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// faceTurnAt is a whole turn played out at t seconds in: listening to a voice coming and going,
// thinking, answering, then the answer lingering on with the voice gone.
func faceTurnAt(start time.Time, t float64) scene {
	heard, reply := "What's the weather tomorrow?", "Tomorrow will be sunny, with a high of 74 and a low of 51."
	voice := func(seed int) *eqView {
		amp := 0.25 + 0.7*math.Abs(math.Sin(t*5.3)*math.Sin(t*1.7+0.4))
		l, p := eqVoice(amp, seed)
		return &eqView{level: l, peak: p}
	}
	now := start.Add(time.Duration(t * float64(time.Second)))
	var s scene
	switch {
	case t < 3:
		s = scene{now: now, phase: "listening", eq: voice(1)}
	case t < 5.5:
		s = scene{now: now, phase: "thinking", heard: heard, eq: eqFor("thinking", false, now)}
	case t < 9:
		s = scene{now: now, phase: "replying", heard: heard, reply: reply, eq: voice(7)}
	default:
		quiet := make([]float64, eqBands)
		s = scene{now: now, phase: "lingering", heard: heard, reply: reply, eq: &eqView{level: quiet, peak: quiet, quiet: true}}
	}
	s.eq.style, s.eq.phase = "face", s.phase
	return s
}

// A turn drawn frame by frame eases from one face into the next and settles once it is over;
// FACE_PREVIEW names a folder to write the frames to, ten a second, to look at without a device.
func TestFaceTurnPlays(t *testing.T) {
	dir := os.Getenv("FACE_PREVIEW")
	faceState.set = false
	start := time.Date(2026, 10, 23, 20, 0, 0, 0, time.Local)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	var last scene
	for i := range 120 {
		last = faceTurnAt(start, float64(i)/10)
		r.draw(last)
		if dir == "" {
			continue
		}
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("face-%03d.png", i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, r.dst); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	if !faceSettled(last.eq) {
		t.Errorf("three seconds into lingering the face is still moving: %+v", faceState.look)
	}
	if faceSettled(&eqView{style: "face", phase: "thinking"}) {
		t.Error("a face smiling after its answer counts as settled for thinking")
	}
}

// A blink closes the eyes and opens them again, and a face with no blinks keeps them open.
func TestFaceBlinks(t *testing.T) {
	shut := 1.0
	for i := range 1000 {
		shut = min(shut, blinkAt(float64(i)/100, 4.2))
	}
	if shut > 0.1 {
		t.Errorf("in ten seconds the eyes closed no further than %.2f", shut)
	}
	for i := range 1000 {
		if o := blinkAt(float64(i)/100, 0); o != 1 {
			t.Fatalf("with no blinks the eyes are %.2f open at %.2fs", o, float64(i)/100)
		}
	}
}
