package home

import (
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// note waits for the next note the rig's feature puts up.
func (r *hintRig) note(t *testing.T, what string) Hint {
	t.Helper()
	select {
	case h := <-r.hints:
		return h
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: no note", what)
	}
	return Hint{}
}

// A station asked for with no Home Assistant connected says that, at once and in its own words: the
// question about the device's settings would be the wrong one. The station is not left starting, and a
// stream that arrives after all takes the note down.
func TestAStationWithNoHomeAssistantSaysSo(t *testing.T) {
	r := newHintRig(t, 50*time.Millisecond)
	stop := component.CallService.Listen(func(c component.Call) {
		if c.Unheard != nil {
			c.Unheard()
		}
	})
	defer stop()
	r.ask(t, "Example FM")
	if h := r.note(t, "no Home Assistant connected"); h.Title != "Radio" || h.Message != NoHomeHint {
		t.Errorf("no Home Assistant connected: the note is %+v", h)
	}
	if c := r.chosen(); c != "" {
		t.Errorf("no Home Assistant connected: still shown as starting %q", c)
	}
	select {
	case h := <-r.hints:
		t.Errorf("a second note followed: %+v", h)
	case <-time.After(200 * time.Millisecond):
	}
	r.f.streamArrived()
	if h := r.note(t, "a stream after all"); h.Message != "" {
		t.Errorf("a stream arrived and the note that came is %+v, want the one that takes it down", h)
	}
}

// A stream that has ended by itself too many times is left off, and the screen is told why.
func TestAStreamLeftOffSaysSo(t *testing.T) {
	r := newHintRig(t, 50*time.Millisecond)
	f := r.f
	f.url, f.asked, f.askedAt, f.stationURL = "http://p/1", "KXYZ", time.Now(), "http://p/1"
	f.resumed, f.resumedAt = resumeTries, time.Now()
	f.ended("http://p/1")
	if h := r.note(t, "the stream left off"); h.Title != "Radio" || h.Message != DroppingHint {
		t.Errorf("the stream left off: the note is %+v", h)
	}
}

// What the notes say is the kind of failure, in a card's three lines, and nothing of an address.
func TestTheRadioNotesAreShortAndPlain(t *testing.T) {
	for _, m := range []string{NoHomeHint, PlayFailedHint, DroppingHint} {
		if len(m) > 110 {
			t.Errorf("%q is %d characters, long for a card", m, len(m))
		}
		for _, bad := range []string{"http", "://", "err"} {
			if strings.Contains(m, bad) {
				t.Errorf("%q has %q in it", m, bad)
			}
		}
	}
}
