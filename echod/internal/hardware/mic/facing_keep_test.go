package mic

import (
	"testing"
	"time"
)

// The measured array's finder runs while somebody keeps it, or for a while after Facing was asked,
// and not otherwise: with another mix chosen, nothing pays for a transform every 8 ms that nobody reads.
func TestFacingWantedOnlyWhileSomebodyReads(t *testing.T) {
	var s Source
	if s.facingWanted() {
		t.Fatal("wanted before anybody asked")
	}
	s.KeepFacing("wake direction", true)
	s.KeepFacing("Toward Talker", true)
	s.KeepFacing("wake direction", false)
	if !s.facingWanted() {
		t.Fatal("not wanted while Toward Talker keeps it")
	}
	s.KeepFacing("Toward Talker", false)
	if s.facingWanted() {
		t.Fatal("still wanted once nobody keeps it")
	}
	s.askedAt.Store(time.Now().UnixNano())
	if !s.facingWanted() {
		t.Fatal("not wanted just after Facing was asked")
	}
	s.askedAt.Store(time.Now().Add(-facingLinger - time.Second).UnixNano())
	if s.facingWanted() {
		t.Fatal("still wanted long after Facing was last asked")
	}
}
