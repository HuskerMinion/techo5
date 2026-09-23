package detect

import "testing"

// The ring gets out of the way for the stop word.
//
// The first thing a ring loop does is duck the radio so the alarm can be heard. Nothing used to duck
// the alarm, so the word whose job is to stop it was the one word always said over a sound that
// nothing would lower.
func TestANearMissOnTheStopWordHushesTheRing(t *testing.T) {
	k := newDeck()
	k.ringing = true
	k.playing = false // Only the ring is sounding; there is no music.

	k.d.heard(StopSlot, 0.65)

	if k.hushes != 1 {
		t.Fatalf("a near miss on the stop word over a ring should hush it once, got %d", k.hushes)
	}
}

// The media setting is about music getting out of the way. An alarm you cannot stop is a different
// thing, so turning that setting off does not take the ring hush with it.
func TestTheRingIsHushedEvenWithNearMissDuckingOff(t *testing.T) {
	k := newDeck()
	k.ringing = true
	k.on = false

	k.d.heard(StopSlot, 0.65)

	if k.hushes != 1 {
		t.Fatalf("the ring hush should not depend on the media setting, got %d hushes", k.hushes)
	}
	if len(k.ducked) != 0 {
		t.Fatalf("the music should not be ducked with the setting off, got %v", k.ducked)
	}
}

// Only the stop word. Any other near miss during a ring is somebody talking, not somebody trying to
// stop the alarm.
func TestOtherSlotsDoNotHushTheRing(t *testing.T) {
	k := newDeck()
	k.ringing = true

	k.d.heard(0, 0.9)

	if k.hushes != 0 {
		t.Fatalf("slot 0 should not hush a ring, got %d", k.hushes)
	}
}

// Nothing ringing, nothing hushed — so a near miss in a quiet room costs nothing.
func TestNoRingNoHush(t *testing.T) {
	k := newDeck()
	k.ringing = false

	k.d.heard(StopSlot, 0.9)

	if k.hushes != 0 {
		t.Fatalf("there is no ring to hush, got %d", k.hushes)
	}
}

// A room that keeps nearly saying it cannot hold an alarm quiet. This is the failure the whole plan
// exists to prevent, so the floor on how often a hush may start is worth a test of its own.
func TestARoomThatKeepsNearlySayingItCannotSilenceTheAlarm(t *testing.T) {
	k := newDeck()
	k.ringing = true

	k.d.heard(StopSlot, 0.65)
	if k.hushes != 1 {
		t.Fatalf("the first near miss should hush, got %d", k.hushes)
	}

	// Hammering it inside the window changes nothing. Nineteen steps of a twentieth stay strictly
	// inside it; the twentieth would land exactly on the boundary, where a hush is due again.
	for range 19 {
		k.tick(ringHushQuiet / 20)
		k.d.heard(StopSlot, 0.65)
	}
	if k.hushes != 1 {
		t.Fatalf("hushes should not stack inside the cool-off, got %d", k.hushes)
	}

	// Past the window it may hush again.
	k.tick(ringHushQuiet)
	k.d.heard(StopSlot, 0.65)
	if k.hushes != 2 {
		t.Fatalf("a near miss after the cool-off should hush again, got %d", k.hushes)
	}
}

// A ring and music together: the ring is hushed and the music is ducked, and neither suppresses the
// other.
func TestARingOverMusicHushesAndDucks(t *testing.T) {
	k := newDeck()
	k.ringing = true
	k.playing = true

	k.d.heard(StopSlot, 0.65)

	if k.hushes != 1 {
		t.Errorf("the ring should be hushed, got %d", k.hushes)
	}
	if len(k.ducked) != 1 || !k.ducked[0] {
		t.Errorf("the music should be ducked too, got %v", k.ducked)
	}
}
