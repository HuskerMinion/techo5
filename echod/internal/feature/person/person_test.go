//go:build !dot

package person

import (
	"testing"
	"time"
)

// cpuOnly is the model on the cores, the VPU let go if the board has one.
func cpuOnly(d *detector) *detector {
	d.close()
	d.vpu = nil
	return d
}

func TestModelLoadsAndScores(t *testing.T) {
	d, err := load()
	if err != nil {
		t.Fatal(err)
	}
	d = cpuOnly(d)
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
}

func BenchmarkScore(b *testing.B) {
	d, err := load()
	if err != nil {
		b.Fatal(err)
	}
	d = cpuOnly(d)
	gray := make([]uint8, side*side)
	for b.Loop() {
		if _, err := d.score(gray); err != nil {
			b.Fatal(err)
		}
	}
}
