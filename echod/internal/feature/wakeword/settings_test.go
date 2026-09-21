package wakeword

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Quiet hours take the tone away. It is the same answer the turn asks for the microphone's history
// and for the hold it keeps out of the tone, so a quiet hour is also what makes a turn send the audio
// somebody spoke on the way out of the wake word.
func TestQuietHoursTakeTheToneAway(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))

	if err := config.Set().Wake(0).Tone(config.ToneChirp); err != nil {
		t.Fatal(err)
	}
	if !Tones(0, false) || !Tones(0, true) {
		t.Fatal("a slot with a tone set makes no sound outside quiet hours")
	}

	// An hour either side of now, so the window covers the current hour whatever minute it is and the
	// test cannot be caught by the clock turning over mid-run. Half the time it crosses midnight, which
	// is the case worth getting right.
	h := time.Now().Hour()
	window := fmt.Sprintf("%d-%d", (h+23)%24, (h+2)%24)
	if err := config.Set().Speaker().QuietHours(window); err != nil {
		t.Fatal(err)
	}
	if Tones(0, false) {
		t.Errorf("quiet hours (%s) left the wake tone in place", window)
	}

	// And back outside them.
	if err := config.Set().Speaker().QuietHours(""); err != nil {
		t.Fatal(err)
	}
	if !Tones(0, false) {
		t.Error("the wake tone did not come back after quiet hours")
	}

	// A slot set to no tone is silent at any hour.
	if err := config.Set().Wake(0).Tone(config.ToneNone); err != nil {
		t.Fatal(err)
	}
	if Tones(0, false) {
		t.Error("a slot set to no tone made a sound")
	}
}

// A follow-up can have a tone of its own: silent, where the wake word's tone is worth having and one
// that plays over the first words of an answer is not, or a different one, which is the thing a
// switch could not say.
func TestAFollowUpCanHaveAToneOfItsOwn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))

	if err := config.Set().Wake(0).Tone(config.ToneChirp); err != nil {
		t.Fatal(err)
	}

	// Nobody has chosen, which is where a device saved before this existed is: a follow-up sounds like
	// the wake word that started the conversation.
	wake := ChimeLength(0, false)
	if !Tones(0, true) {
		t.Fatal("a follow-up does not chime by default")
	}
	if d := ChimeLength(0, true); d != wake {
		t.Errorf("a follow-up with no tone of its own lasted %s, the wake word's %s", d, wake)
	}

	// Silent on its own, without taking the wake word's tone with it.
	if err := config.Set().Wake(0).FollowUpTone(config.ToneNone); err != nil {
		t.Fatal(err)
	}
	if !Tones(0, false) {
		t.Error("silencing the follow-up took the wake word's tone with it")
	}
	if Tones(0, true) {
		t.Error("the follow-up still chimes with its tone set to none")
	}
	// Silent means there is nothing to hold the microphone back for, which is how the turn gets the
	// audio from the instant the reply ended.
	if d := ChimeLength(0, true); d != 0 {
		t.Errorf("a silent follow-up would hold the microphone back for %s", d)
	}

	// And a tone of its own, which is neither the wake word's nor silence.
	if err := config.Set().Wake(0).FollowUpTone(config.ToneDing); err != nil {
		t.Fatal(err)
	}
	if !Tones(0, true) {
		t.Error("a follow-up with a tone of its own makes no sound")
	}
	want := speaker.Length(speaker.WakeTone(config.ToneDing))
	if d := ChimeLength(0, true); d != want {
		t.Errorf("the follow-up lasted %s, want the Ding's %s", d, want)
	}
}
