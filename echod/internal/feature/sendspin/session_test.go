package sendspin

import (
	"testing"
	"time"
)

// What an end gives back, and when. A stream ending is what a skip and a pause both look like from here,
// so a release inside a live session is held back far enough that a skip does not flash the clock, and
// it is not made at all for a pause the room asked for - that one keeps the track on the screen with
// play offered. The connection going is the one case that takes the room back whatever was asked for.
//
// The bug this replaces zeroed the claim before the pause branch returned, so the room was held for the
// life of the daemon: the idle screen stuck on the now-playing page, the media player entity reading
// Paused forever, every transport press going to a session that was gone, and only a restart clearing it.
func TestWhatAnEndGivesBack(t *testing.T) {
	for _, tc := range []struct {
		name      string
		claim     uint64
		asked     string
		now       bool
		wantClaim uint64
		wantAfter time.Duration
	}{
		{"nothing held gives nothing back", 0, "", false, 0, 0},
		{"a stream ending gives the room back after the grace", 1, "", false, 1, changeGrace},
		{"a skip the room asked for still gives it back after the grace", 2, "next", false, 2, changeGrace},
		{"a pause the room asked for holds the room", 3, "pause", false, 0, 0},
		{"the connection going gives it back however it was left", 4, "pause", true, 4, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &session{}
			s.claim = tc.claim
			s.asked.Store(tc.asked)

			claim, after := s.released(tc.now)

			if claim != tc.wantClaim || after != tc.wantAfter {
				t.Errorf("released(now = %v) = (%d, %v), want (%d, %v)",
					tc.now, claim, after, tc.wantClaim, tc.wantAfter)
			}
		})
	}
}
