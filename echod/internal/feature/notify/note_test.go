package notify

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// A note of the device's own goes up as a card, quietly, and Home Assistant hears nothing of it: not
// shown, not tapped away, not run out, not replaced.
func TestANoteIsQuietAndTellsHomeAssistantNothing(t *testing.T) {
	r := newRig(t, true)
	r.f.Note("Radio", home.NoStartHint)
	n, up := r.f.Card()
	if !up || n.Title != "Radio" || n.Message != home.NoStartHint {
		t.Fatalf("card %+v, up %v", n, up)
	}
	if !n.Until.Equal(r.now.Add(noteFor)) {
		t.Errorf("until %v, want %v from now", n.Until, noteFor)
	}
	if !r.f.Dismiss() {
		t.Error("a tap did not take the note down")
	}
	r.f.Note("Radio", home.NoStartHint)
	r.now = r.now.Add(noteFor + time.Second)
	r.f.tick(r.now)
	if _, up := r.f.Card(); up {
		t.Error("the note outlasted its time")
	}
	r.f.Note("Favorites", home.FavoriteHint)
	if err := r.f.Notify("The washing machine is done", "Washer", "", "off", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if got := r.fired(); !same(got, []string{"shown"}) {
		t.Errorf("events %v, want only the notification's", got)
	}
	if r.chimes.Load() != 0 {
		t.Errorf("chimed %d times", r.chimes.Load())
	}
}

// A note never covers a notification Home Assistant sent, and taking a note down leaves one alone.
func TestANoteGivesWayToANotification(t *testing.T) {
	r := newRig(t, true)
	if err := r.f.Notify("The washing machine is done", "Washer", "", "off", "", "", 0); err != nil {
		t.Fatal(err)
	}
	r.f.Note("Radio", home.NoStartHint)
	if n, _ := r.f.Card(); n.Title != "Washer" {
		t.Errorf("the card is %+v, want the notification", n)
	}
	r.f.Note("", "")
	if n, up := r.f.Card(); !up || n.Title != "Washer" {
		t.Errorf("taking the note down took %+v", n)
	}

	// The device's own note, taken down when what it was about came right after all.
	r = newRig(t, true)
	r.f.Note("Radio", home.NoStartHint)
	r.f.Note("", "")
	if _, up := r.f.Card(); up {
		t.Error("the note stayed up")
	}
	if got := r.fired(); len(got) != 0 {
		t.Errorf("events %v", got)
	}
}
