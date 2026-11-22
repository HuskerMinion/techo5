//go:build !dot && !spot && !panel

package privacy

import (
	"errors"
	"io"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
)

// athenaFake stands in for the latch and the converter, and puts everything back afterwards.
type athenaFake struct {
	latched bool
	codec   []bool // every cut asked of the converter, in order
}

func newAthenaFake(t *testing.T) *athenaFake {
	t.Helper()
	f := &athenaFake{}
	latch, grace := athenaLatch, athenaKeyGrace
	athenaLatch = func() (bool, error) { return f.latched, nil }
	athenaKeyGrace = 0
	SetCodec(func(cut bool) error { f.codec = append(f.codec, cut); return nil })
	softwareCut.Store(false)
	athenaLifted.Store(false)
	t.Cleanup(func() {
		athenaLatch, athenaKeyGrace = latch, grace
		codec.Store(nil)
		softwareCut.Store(false)
		athenaLifted.Store(false)
	})
	return f
}

// Home Assistant's mute is the converter and the frames, and Home Assistant can lift it again.
func TestAthenaSoftwareMuteBothWays(t *testing.T) {
	f := newAthenaFake(t)
	m := athenaMute{}
	if err := m.Set(true); err != nil {
		t.Fatal(err)
	}
	if is, _ := m.Get(); !is || !SoftwareCut() || !m.Saved() {
		t.Fatal("muting from Home Assistant left the microphones live")
	}
	if err := m.Set(false); err != nil {
		t.Fatal(err)
	}
	if is, _ := m.Get(); is || SoftwareCut() {
		t.Fatal("unmuting from Home Assistant left the microphones cut")
	}
	if want := []bool{true, false}; len(f.codec) != 2 || f.codec[0] != want[0] || f.codec[1] != want[1] {
		t.Fatalf("converter asked %v, want %v", f.codec, want)
	}
	if m.LiftedByButton() {
		t.Error("an unmute from Home Assistant was taken for the button's")
	}
}

// The latch is the button's: Home Assistant cannot let it go, and asking leaves the software mute be.
func TestAthenaLatchIsTheButtonsAlone(t *testing.T) {
	f := newAthenaFake(t)
	m := athenaMute{}
	f.latched = true
	if is, _ := m.Get(); !is {
		t.Fatal("a set latch read as live")
	}
	if m.Saved() {
		t.Error("the button's latch would be saved as a mute asked for in Home Assistant")
	}
	_ = m.Set(true)
	if err := m.Set(false); !errors.Is(err, ErrButtonOnly) {
		t.Fatalf("unmuting over the latch: %v, want ErrButtonOnly", err)
	}
	if !SoftwareCut() {
		t.Error("a refused unmute took the software mute off under the latch")
	}
}

// Letting the latch go with the button lifts the software mute with it, and says so once.
func TestAthenaButtonReleaseLiftsTheSoftwareMute(t *testing.T) {
	f := newAthenaFake(t)
	m := athenaMute{}
	_ = m.Set(true)
	f.latched = true

	events := []input.Event{
		{Type: input.EvSw, Code: swMuteDevice, Value: 1}, // first press: the latch on top
		{Type: input.EvKey, Code: 248, Value: 1},         // the key itself is not the switch
		{Type: input.EvSw, Code: swMuteDevice, Value: 0}, // second press: the latch lets go
	}
	moves := 0
	athenaFollow(reader(events, func(i int) {
		if i == 2 {
			f.latched = false
		}
	}), func() { moves++ })

	if moves != 2 {
		t.Errorf("handed on %d moves of the latch, want 2", moves)
	}
	if is, _ := m.Get(); is || SoftwareCut() {
		t.Fatal("the button let the latch go and the microphones stayed cut")
	}
	if f.codec[len(f.codec)-1] {
		t.Error("the converter's channels were left off")
	}
	if !m.LiftedByButton() {
		t.Error("the lift was not reported")
	}
	if m.LiftedByButton() {
		t.Error("the lift was reported twice")
	}
}

// A latch let go with no software mute on is only news: nothing is lifted, nothing reported.
func TestAthenaReleaseWithoutSoftwareMute(t *testing.T) {
	newAthenaFake(t)
	m := athenaMute{}
	athenaFollow(reader([]input.Event{{Type: input.EvSw, Code: swMuteDevice, Value: 0}}, nil), func() {})
	if m.LiftedByButton() {
		t.Error("a release with nothing to lift was reported as one")
	}
}

// Muting again after a lift takes back a report nobody has shown yet: it would be about a mute that
// is on again.
func TestAthenaMuteClearsAPendingLift(t *testing.T) {
	newAthenaFake(t)
	m := athenaMute{}
	_ = m.Set(true)
	athenaFollow(reader([]input.Event{{Type: input.EvSw, Code: swMuteDevice, Value: 0}}, nil), func() {})
	_ = m.Set(true)
	if m.LiftedByButton() {
		t.Error("a lift was reported over a mute that is on again")
	}
}

// reader hands out events in order, calling before(i) before each, and then fails.
func reader(events []input.Event, before func(int)) func() (input.Event, error) {
	i := 0
	return func() (input.Event, error) {
		if i >= len(events) {
			return input.Event{}, io.EOF
		}
		if before != nil {
			before(i)
		}
		e := events[i]
		i++
		return e, nil
	}
}
