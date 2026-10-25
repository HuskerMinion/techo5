//go:build !dot && !panel

package touch

import (
	"context"
	"image"
	"io"
	"sync"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
)

// drawn is what one finger along pts (frame pixels, a few apart) comes to, from down to lift. The
// panel's raw frame is the device's (rawFrame in its test file).
func drawn(pts []image.Point) []Kind {
	abs := func(code uint16, v int32) input.Event { return input.Event{Type: input.EvAbs, Code: code, Value: v} }
	syn := input.Event{Type: input.EvSyn, Code: synReport}
	rawW, rawH, toRaw := rawFrame()
	var events []input.Event
	for i, p := range pts {
		rx, ry := toRaw(p.X, p.Y)
		if i == 0 {
			events = append(events, abs(absMTSlot, 0), abs(absMTTrackingID, 1), input.Event{Type: input.EvKey, Code: btnTouch, Value: 1})
		}
		events = append(events, abs(absMTPositionX, int32(rx)), abs(absMTPositionY, int32(ry)), syn)
	}
	events = append(events, abs(absMTTrackingID, -1), input.Event{Type: input.EvKey, Code: btnTouch, Value: 0}, syn)
	s := &Screen{rawW: rawW, rawH: rawH}
	var mu sync.Mutex
	var got []Kind
	s.Gestures.Listen(func(g Gesture) { mu.Lock(); got = append(got, g.Kind); mu.Unlock() })
	read := func() (input.Event, error) {
		if len(events) == 0 {
			return input.Event{}, io.EOF
		}
		e := events[0]
		events = events[1:]
		return e, nil
	}
	_ = s.track(context.Background(), "test", read)
	mu.Lock()
	defer mu.Unlock()
	return got
}

// path is a finger from start along each leg in turn, in steps of a few pixels.
func path(start image.Point, legs ...image.Point) []image.Point {
	pts := []image.Point{start}
	at := start
	for _, l := range legs {
		n := max(abs(l.X), abs(l.Y))/6 + 1
		for i := 1; i <= n; i++ {
			pts = append(pts, at.Add(image.Pt(l.X*i/n, l.Y*i/n)))
		}
		at = at.Add(l)
	}
	return pts
}

func count(got []Kind, k Kind) int {
	n := 0
	for _, g := range got {
		if g == k {
			n++
		}
	}
	return n
}

// The swipes of issue #113, in notches so they hold on every screen: a sideways swipe that drifts up
// or down a little is a sideways swipe and never the volume; an up or down one that drifts sideways
// is still the volume; a slant is never the volume, and a swipe only if it ends clearly sideways; and
// a finger that settles up or down stays that way.
func checkSwipeDirections(t *testing.T) {
	t.Helper()
	n := notch
	right := image.Pt(Width-n/2-1, Height/2)
	center := image.Pt(Width/2, Height/2)
	for _, c := range []struct {
		name       string
		pts        []image.Point
		up, down   int
		left, rght int
		tap        int
	}{
		// 20% up over six notches sideways is more than a notch up: it used to turn the volume, and
		// with that the sideways swipe was lost.
		{name: "left with 20% drift up", pts: path(right, image.Pt(-6*n, -6*n/5)), left: 1},
		{name: "left with 15% drift down", pts: path(right, image.Pt(-6*n, 9*n/10)), left: 1},
		{name: "right with 10% drift", pts: path(image.Pt(n, Height/2), image.Pt(6*n, -6*n/10)), rght: 1},
		// A drift that grows toward the end, after the swipe has settled sideways.
		{name: "left then curling up", pts: path(right, image.Pt(-4*n, 0), image.Pt(-2*n, -3*n/2)), left: 1},
		{name: "up with 15% drift", pts: path(center, image.Pt(n/5, -3*n-n/2)), up: 3},
		{name: "down with 20% drift", pts: path(center, image.Pt(-n*2/5, 2*n+n/2)), down: 2},
		{name: "diagonal", pts: path(center, image.Pt(-2*n, -2*n))},
		{name: "diagonal the other way", pts: path(center, image.Pt(3*n, 3*n))},
		// Settled on a slant, then sideways far enough that it ends clearly sideways: a swipe, still
		// no volume. Before, a slant was lost for good.
		{name: "diagonal then sideways", pts: path(right, image.Pt(-2*n, -2*n), image.Pt(-3*n, 0)), left: 1},
		{name: "diagonal then up", pts: path(image.Pt(Width/2, Height-n/2-1), image.Pt(-2*n, -2*n), image.Pt(0, -3*n))},
		// Settled sideways, then up: no volume, and no swipe either, since where it lifts is not
		// clearly sideways.
		{name: "sideways then up", pts: path(center, image.Pt(-3*n, 0), image.Pt(0, -3*n))},
		// Settled up, then sideways: the volume as far as it went up, and no sideways swipe.
		{name: "up then sideways", pts: path(right, image.Pt(0, -2*n), image.Pt(-5*n, 0)), up: 2},
		// The drawer's edge: a finger there that drifts down is the volume, not a swipe in.
		{name: "edge drifting down", pts: path(right, image.Pt(-n/3, 2*n)), down: 2},
		{name: "edge slanting down", pts: path(right, image.Pt(-2*n, 2*n))},
		{name: "tap with a wobble", pts: path(center, image.Pt(tapMove/2, -tapMove/2)), tap: 1},
	} {
		got := drawn(c.pts)
		if count(got, SwipeUp) != c.up || count(got, SwipeDown) != c.down || count(got, SwipeLeft) != c.left ||
			count(got, SwipeRight) != c.rght || count(got, Tap) != c.tap || len(got) != c.up+c.down+c.left+c.rght+c.tap {
			t.Errorf("%s: got %v, want %d up, %d down, %d left, %d right, %d tap", c.name, got, c.up, c.down, c.left, c.rght, c.tap)
		}
	}
}
