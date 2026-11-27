package nearest

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
)

// A wake word heard for sure and clearly chimes the moment it is claimed, before the window is out, and
// the turn that opens when this device answers is told so, to play no second sound (#727).
func TestASureClearWakeChimesAtOnce(t *testing.T) {
	h := newHouse(t)
	heard := h.now
	h.n.Woke(0)
	if len(h.chimes) != 1 || len(h.opened) != 0 {
		t.Fatalf("on the word: chimed %v, opened %v; want one chime and no turn yet", h.chimes, h.opened)
	}
	h.wait(300 * time.Millisecond)
	if len(h.opened) != 1 || len(h.chimedAt) != 1 || !h.chimedAt[0].Equal(heard) {
		t.Fatalf("answered: opened %v, chimed at %v; want one turn told of the sound at %v", h.opened, h.chimedAt, heard)
	}
	if len(h.chimes) != 1 {
		t.Errorf("chimed %d times, want once", len(h.chimes))
	}
}

// A device another heard the word better than has chimed, and does nothing more: no turn, nothing for
// Home Assistant to hear of (every way to it is a turn), and so nothing listening on the screen or the
// ring.
func TestALosingDeviceGoesQuietAfterItsChime(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.claimFrom("Kitchen", 30)
	h.wait(300 * time.Millisecond)
	if len(h.chimes) != 1 {
		t.Fatalf("chimed %v, want once at the word", h.chimes)
	}
	if len(h.opened) != 0 || len(h.faint) != 0 || h.at0 != 0 {
		t.Errorf("lost, yet opened %v, asked about %v, answered at once %d times", h.opened, h.faint, h.at0)
	}
}

// Told the word was already answered elsewhere, the same.
func TestADeviceToldItWasAnsweredGoesQuietAfterItsChime(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.n.answeredElsewhere(h.mine, "Kitchen")
	h.wait(300 * time.Millisecond)
	if len(h.chimes) != 1 || len(h.opened) != 0 || h.at0 != 0 {
		t.Errorf("chimed %v, opened %v, at once %d; want the chime alone", h.chimes, h.opened, h.at0)
	}
}

// What is left out: a wake that may still not open anything chimes only as its turn opens, as before,
// and one that never contends chimes as its turn opens at once, as it always has.
func TestNoEarlyChimeWhereTheTurnMayNotOpen(t *testing.T) {
	t.Run("faint", func(t *testing.T) {
		h := newHouse(t)
		confirmBelow(t, true, 8)
		h.n.score = func() float64 { return 3.7 }
		h.n.Woke(0)
		h.wait(300 * time.Millisecond)
		if len(h.chimes) != 0 || len(h.faint) != 1 {
			t.Errorf("chimed %v, asked about %v; want no chime and the win asked about", h.chimes, h.faint)
		}
	})
	t.Run("faint, the second wake word, which is not asked about", func(t *testing.T) {
		h := newHouse(t)
		confirmBelow(t, true, 8)
		h.n.score = func() float64 { return 3.7 }
		h.n.Woke(1)
		h.wait(300 * time.Millisecond)
		if len(h.chimes) != 1 || len(h.opened) != 1 || h.chimedAt[0].IsZero() {
			t.Errorf("chimed %v, opened %v at %v; want it chimed and opened", h.chimes, h.opened, h.chimedAt)
		}
	})
	t.Run("over this device's own reply", func(t *testing.T) {
		h := newHouse(t)
		h.n.busy = func() bool { return true }
		h.n.interrupting = func() bool { return true }
		h.n.Woke(0)
		h.wait(300 * time.Millisecond)
		if len(h.chimes) != 0 || len(h.opened) != 1 || !h.chimedAt[0].IsZero() {
			t.Errorf("chimed %v, opened %v told %v; want no early chime and a turn that plays its own", h.chimes, h.opened, h.chimedAt)
		}
	})
	t.Run("held for a command word", func(t *testing.T) {
		h := newHouse(t)
		var opened int
		h.n.WokeGated(0, h.now, func(open func()) { open(); opened++ })
		h.wait(300 * time.Millisecond)
		if len(h.chimes) != 0 || opened != 1 || len(h.chimedAt) != 1 || !h.chimedAt[0].IsZero() {
			t.Errorf("chimed %v, gate opened %d, told %v; want the turn to play its own sound", h.chimes, opened, h.chimedAt)
		}
	})
	t.Run("not sure, confirmed by Home Assistant", func(t *testing.T) {
		h := newHouse(t)
		var answered bool
		h.n.Confirmed(0, h.now, 20, 5, nil, func(answer, _ bool) { answered = answer })
		h.wait(300 * time.Millisecond)
		if len(h.chimes) != 0 || !answered {
			t.Errorf("chimed %v, answered %v; want no early chime", h.chimes, answered)
		}
	})
	t.Run("nearest device off", func(t *testing.T) {
		h := newHouse(t)
		h.n.enabled = false
		h.n.Woke(0)
		if len(h.chimes) != 0 || h.at0 != 1 {
			t.Errorf("chimed %v, answered at once %d; want the turn opened at once as before", h.chimes, h.at0)
		}
	})
	t.Run("nobody to ask", func(t *testing.T) {
		h := newHouse(t)
		h.n.peers = func() []announce.Peer { return nil }
		h.n.Woke(0)
		if len(h.chimes) != 0 || h.at0 != 1 {
			t.Errorf("chimed %v, answered at once %d; want the turn opened at once as before", h.chimes, h.at0)
		}
	})
	t.Run("the voice feature plays none", func(t *testing.T) {
		// Over a ring, for a slot that calls a contact, muted: ChimeNow says it played nothing, and
		// the turn plays its own if this device answers.
		h := newHouse(t)
		h.noChime = true
		h.n.Woke(0)
		h.wait(300 * time.Millisecond)
		if len(h.opened) != 1 || !h.chimedAt[0].IsZero() {
			t.Errorf("opened %v told %v; want a turn that plays its own sound", h.opened, h.chimedAt)
		}
	})
}

// A detector firing again on the same words (#661) is claimed again, but a device that has just chimed
// for them does not chime a second time; past kept it is a new wake word.
func TestARepeatDoesNotChimeAgain(t *testing.T) {
	h := newHouse(t)
	h.n.Woke(0)
	h.claimFrom("Kitchen", 30)
	h.wait(300 * time.Millisecond)
	h.now = h.now.Add(time.Second)
	h.n.Woke(0)
	if len(h.sent) != 2 || len(h.chimes) != 1 {
		t.Fatalf("sent %d claims, chimed %v; want the repeat claimed without a chime", len(h.sent), h.chimes)
	}
	h.wait(300 * time.Millisecond)
	h.now = h.now.Add(kept)
	h.n.Woke(0)
	if len(h.chimes) != 2 {
		t.Errorf("chimed %v; a wake word after kept is a new one", h.chimes)
	}
}

// The sound changes nothing in who answers (#716): a near-tie is still broken by the mean, and the
// claim on the wire is the one made without the sound.
func TestTheChimeLeavesTheNearTieAlone(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name string
		mean float64
		wins bool
	}{{"their mean higher", 5, false}, {"mine higher", 9, true}} {
		for _, quiet := range []bool{false, true} {
			h := newHouse(t)
			h.noChime = quiet
			h.n.score = func() float64 { return 9.6 }
			h.n.mean = func() float64 { return tc.mean }
			h.n.Woke(0)
			h.n.took(claim{From: "Kitchen", Turn: "t", Word: "okay_nabu", Score: 10.4, Mean: f(8)})
			h.wait(300 * time.Millisecond)
			if got := len(h.opened) == 1; got != tc.wins {
				t.Errorf("%s, chime %v: answered %v, want %v", tc.name, !quiet, got, tc.wins)
			}
			c := h.sent[0]
			if c.Score != 9.6 || c.Mean == nil || *c.Mean != tc.mean || c.Word != "okay_nabu" || c.Withdrawn {
				t.Errorf("%s, chime %v: claimed %+v", tc.name, !quiet, c)
			}
		}
	}
}
