package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A device that could not tune wrote the settled false back to itself, so every unit that ever ran
// such a build carries "asp": false whether or not anybody chose it. Once the tuning works, those
// units have to come up tuned rather than staying off for ever.
func TestTheTuningComesBackOnByItselfUnlessSomebodyTurnedItOff(t *testing.T) {
	// What every existing unit looks like: false was written by the settling, never chosen.
	settled := Speaker{ASP: false}
	if !settled.ASPWanted() {
		t.Error("a device that was never able to tune stays untuned after the fix")
	}

	// Somebody who turned it off keeps it off.
	off := Speaker{ASP: false, ASPChosen: true}
	if off.ASPWanted() {
		t.Error("a setting somebody turned off came back on")
	}

	on := Speaker{ASP: true, ASPChosen: true}
	if !on.ASPWanted() {
		t.Error("a setting somebody turned on did not stay on")
	}
}

// A device saved when Chirp was the default moves to Home Assistant's wake sound once, through a real
// file that has never heard of the move; a word set to something else keeps it; after the move, Chirp
// chosen again stays Chirp; and a new device starts on Home Assistant with nothing to move.
func TestWakeSoundsMoveOnce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	old := `{"wake":{"words":[{"id":"alexa","tone":"chirp","follow_up_tone":"chirp"},{"id":"","tone":"ding"}]}}`
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	w := st.Get().Wake.Words
	if w[0].Tone != ToneHA || w[0].FollowUpTone != ToneHA || w[1].Tone != ToneDing {
		t.Fatalf("after the move: %+v", w)
	}
	if err := st.Update(func(c *Config) { c.Wake.Words[0].Tone = ToneChirp }); err != nil {
		t.Fatal(err)
	}
	again, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get().Wake.Words[0].Tone; got != ToneChirp {
		t.Errorf("Chirp chosen after the move came back as %q", got)
	}

	fresh, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.Get().Speaker.SoundsMoved || DefaultTone != ToneHA {
		t.Error("a new device does not start on Home Assistant's wake sound")
	}
}
