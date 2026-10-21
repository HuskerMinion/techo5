package detect

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// What each word does, in each phase of a call, with each setting.
func TestRouteFor(t *testing.T) {
	const wake = 0
	for _, c := range []struct {
		name string
		slot int
		h    heard
		want route
	}{
		// No call: as before any of this.
		{"wake word, no call", wake, heard{}, routeTurn},
		{"stop word, no call", StopSlot, heard{}, routeInterrupt},
		{"stop word, talk-back", StopSlot, heard{talkBack: true}, routeIgnore},
		{"stop word over a ring, talk-back", StopSlot, heard{talkBack: true, ringing: true}, routeEndRing},
		{"wake word, talk-back", wake, heard{talkBack: true, callWake: true}, routeIgnore},

		// The stop word during a call: a ring first, then hanging up only as the hang-up word, and never
		// a call being placed.
		{"stop word over a ring, on a call", StopSlot, heard{phase: phone.Talking, ringing: true, stopHang: true}, routeEndRing},
		{"stop word, on a call, not the hang-up word", StopSlot, heard{phase: phone.Talking}, routeIgnore},
		{"stop word, ringing in, not the hang-up word", StopSlot, heard{phase: phone.Ringing}, routeIgnore},
		{"stop word, on a call", StopSlot, heard{phase: phone.Talking, stopHang: true}, routeHangUp},
		{"stop word, ringing in", StopSlot, heard{phase: phone.Ringing, stopHang: true}, routeHangUp},
		{"stop word, placing a call", StopSlot, heard{phase: phone.Dialing, stopHang: true}, routeIgnore},
		{"stop word, on a call, talk-back", StopSlot, heard{phase: phone.Talking, stopHang: true, talkBack: true}, routeIgnore},

		// The hang-up word goes to hangUp, which has its own checks (hangupHeard).
		{"hang-up word", HangupSlot, heard{phase: phone.Talking}, routeHangUp},

		// A turn open during a call: "stop" cancels the turn, as it would any turn, rather than ending the
		// call; the hang-up word ends both, so "Okay Nabu, hang up" still hangs up. A ring still comes first.
		{"stop word, turn open on a call", StopSlot, heard{phase: phone.Talking, turnOpen: true, stopHang: true}, routeCancelTurn},
		{"stop word, turn open on a call, not the hang-up word", StopSlot, heard{phase: phone.Talking, turnOpen: true}, routeCancelTurn},
		{"stop word over a ring, turn open on a call", StopSlot, heard{phase: phone.Talking, turnOpen: true, ringing: true}, routeEndRing},
		{"hang-up word, turn open on a call", HangupSlot, heard{phase: phone.Talking, turnOpen: true}, routeCancelAndHangUp},
		{"stop word, turn open, no call", StopSlot, heard{turnOpen: true}, routeInterrupt},

		// A wake word during a call, only with Wake word during calls on.
		{"wake word, on a call, off", wake, heard{phase: phone.Talking}, routeIgnore},
		{"wake word, on a call", wake, heard{phase: phone.Talking, callWake: true}, routeTurn},
		{"wake word, ringing in", wake, heard{phase: phone.Ringing, callWake: true}, routeTurn},
		{"wake word, placing a call", wake, heard{phase: phone.Dialing, callWake: true}, routeIgnore},
		{"wake word, the far end talking", wake, heard{phase: phone.Talking, callWake: true, farEnd: true}, routeFarEnd},
	} {
		if got := routeFor(c.slot, c.h); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

// With the settings as a device starts with them, nothing said during a call ends it, and only the
// stop word over a ring does anything at all: hanging up by voice is chosen, never the default.
func TestNothingHangsUpByDefault(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	c := config.Get()
	if c.Wake.Hangup.On() || c.Home.CallWake {
		t.Fatalf("on by default: hang-up word %q, wake word during calls %v", c.Wake.Hangup.ID, c.Home.CallWake)
	}
	for _, phase := range []phone.Phase{phone.Ringing, phone.Dialing, phone.Talking} {
		h := heard{phase: phase, stopHang: c.Wake.Hangup.StopWord(), callWake: c.Home.CallWake}
		for _, slot := range []int{0, StopSlot} {
			if got := routeFor(slot, h); got != routeIgnore {
				t.Errorf("phase %v, slot %d: got %d, want ignored", phase, slot, got)
			}
		}
		if hangupHeard(phase, false, false, false, false) != phone.Answerable(phase) {
			t.Errorf("phase %v: the hang-up word acts where a call cannot be ended by voice", phase)
		}
	}
	if hangupHeard(phone.Dialing, false, false, false, false) {
		t.Error("the hang-up word ends a call being placed")
	}
}
