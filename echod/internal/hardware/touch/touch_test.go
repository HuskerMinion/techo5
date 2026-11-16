//go:build !dot && !spot && !panel

package touch

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
)

// A second tap on exactly the spot of the first arrives with no position: the kernel leaves out a
// slot's X and Y when they have not changed. It is still a tap, where the first one was.
func TestATapWhereTheLastLiftedStillCounts(t *testing.T) {
	defer func(w, h int) { Width, Height = w, h }(Width, Height)
	abs := func(code uint16, v int32) input.Event { return input.Event{Type: input.EvAbs, Code: code, Value: v} }
	syn := input.Event{Type: input.EvSyn, Code: synReport}
	events := []input.Event{
		abs(absMTSlot, 0), abs(absMTTrackingID, 1), abs(absMTPositionX, 400), abs(absMTPositionY, 600), syn,
		abs(absMTTrackingID, -1), syn,
		abs(absMTTrackingID, 2), syn, // the same place: no X, no Y
		abs(absMTTrackingID, -1), syn,
	}
	s := &Screen{rawW: 800, rawH: 1280}
	var got []Gesture
	s.Gestures.Listen(func(g Gesture) { got = append(got, g) })
	read := func() (input.Event, error) {
		if len(events) == 0 {
			return input.Event{}, io.EOF
		}
		e := events[0]
		events = events[1:]
		return e, nil
	}
	_ = s.track(context.Background(), "test", read)
	if len(got) != 2 || got[0].Kind != Tap || got[1] != got[0] {
		t.Fatalf("two taps in one place gave %v", got)
	}
}

// Lost events (SYN_DROPPED) leave the remembered positions wrong, so a finger that was down is let go
// with nothing reported, what arrives until the next report is skipped, and the next touch starts
// clean: a tap where it really lands, not a swipe from somewhere stale.
func TestLostEventsForgetTheFinger(t *testing.T) {
	defer func(w, h int) { Width, Height = w, h }(Width, Height)
	abs := func(code uint16, v int32) input.Event { return input.Event{Type: input.EvAbs, Code: code, Value: v} }
	syn := input.Event{Type: input.EvSyn, Code: synReport}
	dropped := input.Event{Type: input.EvSyn, Code: synDropped}
	events := []input.Event{
		abs(absMTSlot, 0), abs(absMTTrackingID, 1), abs(absMTPositionX, 400), abs(absMTPositionY, 600), syn,
		dropped,
		abs(absMTPositionY, 100), abs(absMTTrackingID, -1), syn, // part of the damaged frame: skipped
		abs(absMTTrackingID, 2), abs(absMTPositionX, 200), abs(absMTPositionY, 300), syn,
		abs(absMTTrackingID, -1), syn,
	}
	s := &Screen{rawW: 800, rawH: 1280}
	var got []Gesture
	s.Gestures.Listen(func(g Gesture) { got = append(got, g) })
	read := func() (input.Event, error) {
		if len(events) == 0 {
			return input.Event{}, io.EOF
		}
		e := events[0]
		events = events[1:]
		return e, nil
	}
	_ = s.track(context.Background(), "test", read)
	if len(got) != 1 || got[0].Kind != Tap {
		t.Fatalf("after lost events, want one tap from the next touch; got %v", got)
	}
	wantX, wantY := s.landscape(200, 300)
	if got[0].X != wantX || got[0].Y != wantY {
		t.Fatalf("the tap landed at (%d,%d), want (%d,%d) where it was", got[0].X, got[0].Y, wantX, wantY)
	}
}

// A finger held still on the Show is a tap, unless holds are asked for (the night light): then it is a
// Hold and a Release, and no tap - the press that lifts the night light must not also press what
// comes up under it.
func TestAShowHoldOnlyWhenAskedFor(t *testing.T) {
	defer func(w, h int) { Width, Height = w, h }(Width, Height)
	press := func(holds bool) []Gesture {
		abs := func(code uint16, v int32) input.Event { return input.Event{Type: input.EvAbs, Code: code, Value: v} }
		syn := input.Event{Type: input.EvSyn, Code: synReport}
		events := []input.Event{
			abs(absMTSlot, 0), abs(absMTTrackingID, 1), abs(absMTPositionX, 400), abs(absMTPositionY, 600), syn,
			abs(absMTTrackingID, -1), syn,
		}
		s := &Screen{rawW: 800, rawH: 1280}
		s.SetHolds(holds)
		// The Hold comes from the hold timer's goroutine.
		var mu sync.Mutex
		var got []Gesture
		s.Gestures.Listen(func(g Gesture) { mu.Lock(); got = append(got, g); mu.Unlock() })
		read := func() (input.Event, error) {
			if len(events) == 0 {
				return input.Event{}, io.EOF
			}
			if len(events) == 2 { // the lift, after the finger has stayed
				time.Sleep(holdAfter + 150*time.Millisecond)
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
	if got := press(false); len(got) != 1 || got[0].Kind != Tap {
		t.Errorf("a long press without holds gave %v, want a tap", got)
	}
	// A hold that came of staying put does not say it moved.
	if got := press(true); len(got) != 2 || got[0].Kind != Hold || got[0].Moved || got[1].Kind != Release {
		t.Errorf("a long press with holds gave %v, want hold, release", got)
	}
}

// A finger coming down says where, once and before anything it does: the display shows a press under
// it. In follow mode a finger that moves is a Hold marked as moved, so a page that opens something on a
// long press does not take a slide for one.
func TestAFingerSaysWhereItLanded(t *testing.T) {
	defer func(w, h int) { Width, Height = w, h }(Width, Height)
	abs := func(code uint16, v int32) input.Event { return input.Event{Type: input.EvAbs, Code: code, Value: v} }
	syn := input.Event{Type: input.EvSyn, Code: synReport}
	events := []input.Event{
		abs(absMTSlot, 0), abs(absMTTrackingID, 1), abs(absMTPositionX, 400), abs(absMTPositionY, 600), syn,
		abs(absMTPositionY, 600+followMove+8), syn,
		abs(absMTTrackingID, -1), syn,
	}
	s := &Screen{rawW: 800, rawH: 1280}
	s.SetFollow(true)
	var got []Gesture
	var landed []Point
	var order []string
	s.Gestures.Listen(func(g Gesture) { got = append(got, g); order = append(order, string(g.Kind)) })
	s.Landed.Listen(func(p Point) { landed = append(landed, p); order = append(order, "landed") })
	s.Down.Listen(func(v bool) {
		if !v {
			order = append(order, "up")
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
	wantX, wantY := s.landscape(400, 600)
	if len(landed) != 1 || landed[0] != (Point{X: wantX, Y: wantY}) {
		t.Fatalf("the finger landed at %v, want once at (%d,%d)", landed, wantX, wantY)
	}
	if len(got) != 3 || got[0].Kind != Hold || !got[0].Moved || got[1].Kind != Drag || got[2].Kind != Release {
		t.Fatalf("a finger that moved in follow mode gave %v, want a moved hold, a drag and a release", got)
	}
	if got[1].Moved || got[2].Moved {
		t.Errorf("only the hold says it moved: %v", got)
	}
	if len(order) != 5 || order[0] != "landed" || order[4] != "up" {
		t.Errorf("the order was %v, want landed first and the lift last", order)
	}
}
