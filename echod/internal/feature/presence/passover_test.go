//go:build !dot

package presence

import (
	"bytes"
	"log/slog"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Each look the detector passes over is counted by why: the whole picture swinging, too bright, and
// compared looks by how much moved.
func TestLooksPassedOverAreCountedByKind(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	var d detector
	var p passStats
	at := time.Unix(1000, 0)
	take := func(g []uint8) {
		d.step(g)
		p.add(at, d.last, d.last.kind == lookCounted && d.last.patch >= d.need())
		at = at.Add(500 * time.Millisecond)
	}
	take(room(r, -1, 0)) // nothing to compare with
	for range 10 {
		take(room(r, -1, 0))
	}
	take(room(r, -1, 80)) // a light switched on
	glary := make([]uint8, gridW*gridH)
	for i := range glary {
		glary[i] = 240
	}
	take(glary) // the swing to it
	take(glary) // and too bright
	take(room(r, -1, 0))

	if p.looks != 15 || p.counted != 10 || p.swing != 3 || p.bright != 1 {
		t.Errorf("looks %d, counted %d, swing %d, bright %d; want 15, 10, 3, 1",
			p.looks, p.counted, p.swing, p.bright)
	}
	if p.moved != 0 || p.peak >= d.need()/2 {
		t.Errorf("a still room: moved %d, peak %d; want 0 and under half of %d", p.moved, p.peak, d.need())
	}
	if p.worth(d.need()) {
		t.Error("four looks passed over and nothing moving is not worth a line")
	}

	// Somebody walking in: the patch that moved is the peak.
	for i := range 3 {
		take(room(r, 2+i*4, 0))
	}
	if p.peak < d.need() || p.moved == 0 || !p.worth(d.need()) {
		t.Errorf("somebody walking: peak %d, moved %d (need %d); want the peak at need and a line",
			p.peak, p.moved, d.need())
	}
}

// Hushed frames come at the camera's rate, up to eight a second with gestures on, and are counted at
// the room's.
func TestHushedFramesAreCountedAtTheRoomsRate(t *testing.T) {
	var p passStats
	at := time.Unix(1000, 0)
	for range 16 { // two seconds at eight a second
		p.hush(at)
		at = at.Add(125 * time.Millisecond)
	}
	if p.hushed < 4 || p.hushed > 5 {
		t.Errorf("hushed %d over two seconds; want 4 or 5", p.hushed)
	}
}

// A minute is summed up once it is over and not before, one line a minute at most, and a minute with
// nothing to explain says nothing.
func TestPassedOverIsSaidAtMostOnceAMinute(t *testing.T) {
	var p passStats
	start := time.Unix(1000, 0)
	lines := 0
	// Ten minutes of a look every half second, the picture swinging in one of five.
	for i := range 1200 {
		at := start.Add(time.Duration(i) * 500 * time.Millisecond)
		l := look{kind: lookCounted, patch: 3}
		if i%5 == 0 {
			l = look{kind: lookSwing}
		}
		p.add(at, l, false)
		if i%2 == 1 { // the second's tick
			if s, worth := p.due(at, defaultPatch); worth {
				lines++
				if s.swing < 20 || s.looks < 110 {
					t.Errorf("line %d: swing %d of %d looks; want a minute's", lines, s.swing, s.looks)
				}
			}
		}
	}
	if lines < 9 || lines > 10 {
		t.Errorf("%d lines over ten minutes; want one a minute", lines)
	}

	// A still room: no line, minute after minute.
	p.reset()
	for i := range 1200 {
		at := start.Add(time.Duration(i) * 500 * time.Millisecond)
		p.add(at, look{kind: lookCounted, patch: 2}, false)
		if _, worth := p.due(at, defaultPatch); worth {
			t.Fatalf("look %d: a still room said something", i)
		}
	}
}

// Through the feature: a minute of the picture swinging while nobody is near is one line with the
// count, and nothing is said once somebody is.
func TestPassedOverLine(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Presence().On(true); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	r := rand.New(rand.NewPCG(7, 8))
	f := Get()
	f.mu.Lock()
	f.det, f.skip, f.present, f.lastRoom, f.hushUntil = newDetector(50), 0, false, time.Time{}, time.Time{}
	f.passed.reset()
	f.mu.Unlock()
	start := time.Now()
	for i := range 150 { // 75 seconds
		at := start.Add(time.Duration(i) * 500 * time.Millisecond)
		light := 0
		if i%4 == 0 {
			light = 80
		}
		f.take(room(r, -1, light), at)
		if i%2 == 1 {
			f.expire(at)
		}
	}
	out := buf.String()
	if n := strings.Count(out, "presence: passing over looks"); n != 1 {
		t.Fatalf("%d lines in 75 seconds; want 1:\n%s", n, out)
	}
	for _, want := range []string{"need=20", "bar=25", "bright=0", "hushed=0"} {
		if !strings.Contains(out, want) {
			t.Errorf("the line has no %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "swing=0 ") {
		t.Errorf("the line counts no swing:\n%s", out)
	}
}
