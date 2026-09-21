package wakeword

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Quiet hours take the tone away. It is the same answer the turn asks for the microphone's history
// and for the hold it keeps out of the tone, so a quiet hour is also what makes a turn send the audio
// somebody spoke on the way out of the wake word.
func TestQuietHoursTakeTheToneAway(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))

	if err := config.Set().Wake(0).Tone(config.ToneChirp); err != nil {
		t.Fatal(err)
	}
	if !Tones(0) {
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
	if Tones(0) {
		t.Errorf("quiet hours (%s) left the wake tone in place", window)
	}

	// And back outside them.
	if err := config.Set().Speaker().QuietHours(""); err != nil {
		t.Fatal(err)
	}
	if !Tones(0) {
		t.Error("the wake tone did not come back after quiet hours")
	}

	// A slot set to no tone is silent at any hour.
	if err := config.Set().Wake(0).Tone(config.ToneNone); err != nil {
		t.Fatal(err)
	}
	if Tones(0) {
		t.Error("a slot set to no tone made a sound")
	}
}
