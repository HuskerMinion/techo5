//go:build !dot && !panel

package touch

import (
	"context"
	"image"
	"io"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
)

// A finger follow mode took (a Hold, then a Drag) that a page hands back (Unfollow) as its first Drag
// comes is a swipe from then on: a swipe down from where it landed goes out as the notches it has
// traveled, and its lift is no Release. A finger after it is followed again.
func TestAFingerHandedBackIsASwipe(t *testing.T) {
	abs := func(code uint16, v int32) input.Event { return input.Event{Type: input.EvAbs, Code: code, Value: v} }
	syn := input.Event{Type: input.EvSyn, Code: synReport}
	rawW, rawH, toRaw := rawFrame()
	var events []input.Event
	finger := func(pts []image.Point) {
		for i, p := range pts {
			rx, ry := toRaw(p.X, p.Y)
			if i == 0 {
				events = append(events, abs(absMTSlot, 0), abs(absMTTrackingID, 1), input.Event{Type: input.EvKey, Code: btnTouch, Value: 1})
			}
			events = append(events, abs(absMTPositionX, int32(rx)), abs(absMTPositionY, int32(ry)), syn)
		}
		events = append(events, abs(absMTTrackingID, -1), input.Event{Type: input.EvKey, Code: btnTouch, Value: 0}, syn)
	}
	start := image.Pt(Width/2, Height/4)
	finger(path(start, image.Pt(0, 3*notch+notch/2)))
	finger(path(start, image.Pt(0, 3*notch+notch/2)))

	s := &Screen{rawW: rawW, rawH: rawH}
	s.SetFollow(true)
	var got []Kind
	handed := 0
	s.Gestures.Listen(func(g Gesture) {
		got = append(got, g.Kind)
		if g.Kind == Drag && handed == 0 {
			handed++
			s.Unfollow()
		}
	})
	read := func() (input.Event, error) {
		if len(events) == 0 {
			return input.Event{}, io.EOF
		}
		e := events[0]
		events = events[1:]
		return e, nil
	}
	_ = s.track(context.Background(), "test", read)

	// The first finger: a moved Hold and a Drag, then three notches down and nothing at the lift.
	first, second := got, []Kind(nil)
	for i, k := range got {
		if i > 0 && k == Hold {
			first, second = got[:i], got[i:]
			break
		}
	}
	if len(first) < 2 || first[0] != Hold || first[1] != Drag || count(first, SwipeDown) != 3 || count(first, Release) != 0 || count(first, Drag) != 1 {
		t.Errorf("the finger handed back gave %v, want a hold, a drag and three swipes down", first)
	}
	if len(second) == 0 || second[0] != Hold || count(second, Release) != 1 || count(second, SwipeDown) != 0 {
		t.Errorf("the finger after it gave %v, want it followed", second)
	}
}
