//go:build !dot

package person

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestModelLoadsAndScores(t *testing.T) {
	d, err := load()
	if err != nil {
		t.Fatal(err)
	}
	gray := make([]uint8, side*side)
	for i := range gray {
		gray[i] = uint8(i % 251)
	}
	p, err := d.score(gray)
	if err != nil {
		t.Fatal(err)
	}
	if p < 0 || p > 1 {
		t.Fatalf("a score of %v", p)
	}
	// The same picture, the same score: nothing is carried from one look to the next.
	q, _ := d.score(gray)
	if p != q {
		t.Fatalf("%v then %v", p, q)
	}
	if _, err := d.score(gray[:10]); err == nil {
		t.Fatal("a picture of the wrong size was scored")
	}
}

// TestModelTellsAPersonFromNone runs the model on TensorFlow Lite Micro's own two pictures (testdata), a
// person and no person (a giraffe among trees), 96 by 96 shades of gray as picture makes them.
func TestModelTellsAPersonFromNone(t *testing.T) {
	d, err := load()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		file   string
		person bool
	}{{"person.gray", true}, {"no_person.gray", false}} {
		gray, err := os.ReadFile(filepath.Join("testdata", c.file))
		if err != nil {
			t.Fatal(err)
		}
		p, err := d.score(gray)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %.3f", c.file, p)
		// threshold is 0.6: the person well over it, the giraffe well under.
		if c.person && p < 0.8 || !c.person && p > 0.4 {
			t.Errorf("%s scored %.3f", c.file, p)
		}
	}
}

func TestPictureIsTheMiddleSquareBrightened(t *testing.T) {
	w := side * 4 / 3
	g := make([]uint8, w*side)
	for y := range side {
		for x := range w {
			g[y*w+x] = uint8(x)
		}
	}
	out := square(g, w)
	if len(out) != side*side {
		t.Fatalf("%d pixels", len(out))
	}
	// The middle columns, not the whole width squeezed: the first is column (w-side)/2.
	if out[0] != gamma[(w-side)/2] || out[side-1] != gamma[(w-side)/2+side-1] {
		t.Fatalf("columns %d to %d, want %d to %d", out[0], out[side-1], gamma[(w-side)/2], gamma[(w-side)/2+side-1])
	}
	// Black and white stay, and the middle comes up: 18% gray is about half way in a photograph.
	if gamma[0] != 0 || gamma[255] != 255 || gamma[46] < 112 || gamma[46] > 124 {
		t.Fatalf("gamma 0, 46, 255: %d, %d, %d", gamma[0], gamma[46], gamma[255])
	}
	for i := 1; i < 256; i++ {
		if gamma[i] < gamma[i-1] {
			t.Fatalf("gamma falls at %d", i)
		}
	}
}

func TestConfidenceUnknownWhenNotLooking(t *testing.T) {
	f := Get()
	f.confidence.Set(42)
	f.forget()
	if !math.IsNaN(float64(f.confidence.Get())) {
		t.Fatalf("confidence %v, want none", f.confidence.Get())
	}
}

func TestJudge(t *testing.T) {
	var j judge
	at := time.Unix(1000, 0)
	j.add(0.9, at)
	j.add(0.9, at)
	if j.seen(at) {
		t.Fatal("seen before enough looks")
	}
	j.add(0.9, at)
	if !j.seen(at) || !j.seen(at.Add(hold-time.Second)) || j.seen(at.Add(hold)) {
		t.Fatal("a sighting not held for hold")
	}

	// One high score among low ones is not a person.
	var k judge
	for _, p := range []float32{0.2, 0.95, 0.3} {
		k.add(p, at)
	}
	if k.seen(at) {
		t.Fatal("one look in three made a person")
	}
	if m := k.mean(); math.Abs(float64(m)-(0.2+0.95+0.3)/3) > 1e-6 {
		t.Fatalf("mean %v", m)
	}
}

func BenchmarkScore(b *testing.B) {
	d, err := load()
	if err != nil {
		b.Fatal(err)
	}
	gray := make([]uint8, side*side)
	for b.Loop() {
		if _, err := d.score(gray); err != nil {
			b.Fatal(err)
		}
	}
}
