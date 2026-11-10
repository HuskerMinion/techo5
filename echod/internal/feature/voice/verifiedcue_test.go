package voice

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestVerifiedCueShowsListeningBeforeOpeningAssist(t *testing.T) {
	w := newWatcher(t, config.OnHA{})
	if err := config.Set().OnHA().VerifySTT(true); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Wake(0).Tone(config.ToneNone); err != nil {
		t.Fatal(err)
	}
	cue := &verifiedCue{ctx: t.Context(), slot: 0, done: make(chan struct{})}
	w.startVerifiedCue(cue)
	if w.cue != cue || w.shown.Phase != phaseListening.String() ||
		w.Phase() != phaseIdle || len(w.out) != 0 {
		t.Fatal("the approved cue should show listening while Assist is still idle")
	}
	select {
	case <-cue.result:
	case <-time.After(time.Second):
		t.Fatal("cue did not complete")
	}
	w.finishVerifiedCue(time.Time{})
	if w.cue != nil || w.shown.Phase != phaseIdle.String() {
		t.Fatal("canceling the cue should restore the idle display")
	}
}

func TestVerifiedTurnWaitsForAssistRunBeforeSendingAudio(t *testing.T) {
	w := newWatcher(t, config.OnHA{VerifySTT: true})
	ready := make(chan struct{})
	w.verifiedAudioReady = ready
	w.handle(event{kind: evSpeaking})
	select {
	case <-ready:
		t.Fatal("audio was released before Assist opened the run")
	default:
	}
	w.handle(event{kind: evRunStart})
	select {
	case <-ready:
	default:
		t.Fatal("audio was held after Assist opened the run")
	}
	if w.verifiedAudioReady != nil {
		t.Fatal("the completed gate would leak into the next turn")
	}
}
