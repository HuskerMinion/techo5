package config

import "testing"

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

// A device saved when Chirp was the default moves to Home Assistant's wake sound once, a word set to
// something else keeps it, and after the move Chirp chosen again stays Chirp.
func TestWakeSoundsMoveOnce(t *testing.T) {
	c := Config{Wake: Wake{Words: []WakeWord{{Tone: ToneChirp, FollowUpTone: ToneChirp}, {Tone: ToneDing}}}}
	c.moveSounds()
	if c.Wake.Words[0].Tone != ToneHA || c.Wake.Words[0].FollowUpTone != ToneHA || c.Wake.Words[1].Tone != ToneDing {
		t.Fatalf("after the move: %+v", c.Wake.Words)
	}
	c.Wake.Words[0].Tone = ToneChirp
	c.moveSounds()
	if c.Wake.Words[0].Tone != ToneChirp {
		t.Error("Chirp chosen after the move was moved again")
	}
	if !defaultSpeaker().SoundsMoved || DefaultTone != ToneHA {
		t.Error("a new device starts on Chirp")
	}
}
