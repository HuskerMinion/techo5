package buttons

import (
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// Home Assistant hears a press for every tap, and a double or triple press after it for the action
// button only. A double tap of another button is not a third press.
func TestEventType(t *testing.T) {
	for _, c := range []struct {
		ev   buttons.Event
		want string
		ok   bool
	}{
		{buttons.Event{Name: buttons.Action, Kind: buttons.Tap}, Press, true},
		{buttons.Event{Name: buttons.Action, Kind: buttons.Hold}, Hold, true},
		{buttons.Event{Name: buttons.Action, Kind: buttons.LongHold}, LongHold, true},
		{buttons.Event{Name: buttons.Action, Kind: buttons.DoubleTap}, DoublePress, true},
		{buttons.Event{Name: buttons.Action, Kind: buttons.TripleTap}, TriplePress, true},
		{buttons.Event{Name: buttons.Mute, Kind: buttons.Tap}, Press, true},
		{buttons.Event{Name: buttons.Mute, Kind: buttons.DoubleTap}, "", false},
		{buttons.Event{Name: buttons.Mute, Kind: buttons.TripleTap}, "", false},
		{buttons.Event{Name: buttons.VolumeUp, Kind: buttons.Repeat}, "", false},
	} {
		got, ok := eventType(c.ev)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%v: got %q, %v; want %q, %v", c.ev, got, ok, c.want, c.ok)
		}
	}
}

// Only the action button lists double and triple presses; the others keep the types they had.
func TestOnlyTheActionButtonListsMultiplePresses(t *testing.T) {
	e := Get()
	for name, ev := range e.events {
		multi := slices.Contains(ev.Types, DoublePress) || slices.Contains(ev.Types, TriplePress)
		if name == buttons.Action && (!slices.Contains(ev.Types, DoublePress) || !slices.Contains(ev.Types, TriplePress)) {
			t.Errorf("the action button lists %v, want double_press and triple_press among them", ev.Types)
		}
		if name != buttons.Action && multi {
			t.Errorf("the %s button lists %v, want no double or triple press", name, ev.Types)
		}
		if !slices.Contains(ev.Types, Press) || !slices.Contains(ev.Types, Hold) {
			t.Errorf("the %s button lists %v, want press and hold kept", name, ev.Types)
		}
	}
}
