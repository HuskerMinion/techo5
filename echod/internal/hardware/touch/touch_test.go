//go:build !dot && !spot

package touch

import (
	"context"
	"io"
	"testing"

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
