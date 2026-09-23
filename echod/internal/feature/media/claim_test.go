package media

import (
	"sync"
	"testing"
)

// The server chooses the order: Music Assistant ends one stream and starts the next in whichever order
// it likes. A session that is finishing must not free the speaker that the session taking over holds,
// which is what left the room unable to name anything after the first track.
func TestAnOlderClaimCannotFreeTheSpeaker(t *testing.T) {
	var c claim

	first := c.take()
	second := c.take()
	if !c.playing() {
		t.Fatal("taken twice, and the speaker is free")
	}

	// The first stream ends, after the second one has started.
	if c.letGo(first) {
		t.Fatal("an older claim gave back a speaker a newer one holds")
	}
	if !c.playing() {
		t.Fatal("the speaker was freed by a claim that no longer holds it")
	}

	if !c.letGo(second) {
		t.Fatal("the holder could not give the speaker back")
	}
	if c.playing() {
		t.Fatal("given back, and still held")
	}
}

// A claim given back twice is not a way in for the first one again.
func TestAClaimGivenBackIsDone(t *testing.T) {
	var c claim

	n := c.take()
	if !c.letGo(n) {
		t.Fatal("the holder could not give the speaker back")
	}
	if c.letGo(n) {
		t.Fatal("the same claim gave the speaker back twice")
	}
	if c.playing() {
		t.Fatal("the speaker is held after being given back")
	}
}

// The two values have to move together, and a test that takes and lets go in order cannot see that they
// do not. This races the taker against the holder being torn down, which is the window a skip opens: the
// server starts the next stream while the one before it is still ending. Storing the hold before taking
// the number loses the speaker here - the older letGo reads the older number, matches it, and clears a
// hold the new taker has just set - so the loop is the assertion, and the race detector sees nothing
// because two atomics are individually fine.
func TestANewClaimSurvivesAnOlderOneLettingGo(t *testing.T) {
	for range 20000 {
		var c claim
		older := c.take()

		var (
			wg    sync.WaitGroup
			newer uint64
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			newer = c.take()
		}()
		go func() {
			defer wg.Done()
			c.letGo(older)
		}()
		wg.Wait()

		if !c.playing() {
			t.Fatal("an older claim let go of a speaker a newer one had taken")
		}
		if !c.letGo(newer) {
			t.Fatal("the newer claim could not give the speaker back")
		}
		if c.playing() {
			t.Fatal("given back, and still held")
		}
	}
}
