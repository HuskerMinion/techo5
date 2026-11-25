package nearest

import (
	"testing"
	"time"
)

// A wake word said over this device's own reply, or while it thinks, takes the turn over, and so it is
// claimed, leading by onCallLead (#737): it wins a word another device barely heard, or heard about as
// well, and loses only to one that heard it clearly better. Heard over its own reply, it is opened as
// an interruption always was, never asked about as faint.
func TestAWakeOverAReplyIsClaimedWithALead(t *testing.T) {
	for _, c := range []struct {
		name   string
		theirs float64
		opened bool
	}{
		{"barely heard elsewhere", 1.1, true},
		{"heard about as well elsewhere", 4 + onCallLead - 1, true},
		{"heard clearly better elsewhere", 4 + onCallLead + 1, false},
	} {
		h := newHouse(t)
		confirmBelow(t, true, 8)
		h.n.busy = func() bool { return true }
		h.n.interrupting = func() bool { return true }
		h.n.score = func() float64 { return 4 } // the word, over the device's own reply
		heard := h.now
		h.n.Woke(0)
		if h.at0 != 0 || len(h.sent) != 1 || h.sent[0].Score != 4+onCallLead {
			t.Fatalf("%s: answered at once %d times, claims sent %+v; want one claim at %v", c.name, h.at0, h.sent, 4+onCallLead)
		}
		h.claimFrom("Kitchen", c.theirs)
		h.wait(300 * time.Millisecond)
		if got := len(h.opened) == 1; got != c.opened || len(h.faint) != 0 {
			t.Errorf("%s: opened %v (asked about %d), want opened %v", c.name, got, len(h.faint), c.opened)
		}
		if c.opened && !h.opened[0].Equal(heard) {
			t.Errorf("%s: opened from %v, want from the word at %v", c.name, h.opened[0], heard)
		}
	}
}

// The device that barely heard the word, idle in another room, stands down to the one being
// interrupted: its claim, lead and all, is the one every device weighs the same way, an older one too.
func TestTheDeviceBarelyHearingStandsDownToTheOneTalking(t *testing.T) {
	h := newHouse(t)
	h.n.score = func() float64 { return 1.1 }
	h.n.Woke(0)
	h.claimFrom("Kitchen", 4+onCallLead) // the claim the talking device sends
	h.wait(300 * time.Millisecond)
	if len(h.opened) != 0 || h.at0 != 0 {
		t.Errorf("opened %d (at once %d): the faint device answered over the one talking", len(h.opened), h.at0)
	}
}

// A wake word while listening is part of the sentence and is ignored, so it is still not claimed; nor
// is any word but a wake word over a turn, such as the next song word.
func TestOnlyAWakeThatTakesTheTurnOverIsClaimed(t *testing.T) {
	h := newHouse(t)
	h.n.busy = func() bool { return true }
	h.n.Woke(0)
	if h.at0 != 1 || len(h.sent) != 0 {
		t.Errorf("listening: answered at once %d times, claims %v", h.at0, h.sent)
	}

	h = newHouse(t)
	h.n.busy = func() bool { return true }
	h.n.interrupting = func() bool { return true }
	acted := 0
	h.n.Answer("command:next_song_word", func() bool { return true }, func() { acted++ })
	if acted != 1 || len(h.sent) != 0 {
		t.Errorf("the next song word over a reply: acted %d, claims %v; want acted at once", acted, h.sent)
	}
}
