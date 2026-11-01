package voice

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/endpoint"
)

// voiceAt is n tenths of a second of a 300 Hz tone at an RMS level, standing in for a voice.
func voiceAt(tenths int, rms float64) []int16 {
	s := make([]int16, tenths*endpoint.Window)
	for i := range s {
		s[i] = int16(rms * math.Sqrt2 * math.Sin(2*math.Pi*300*float64(i)/16000))
	}
	return s
}

// A second's pause to think ends the turn at the default, and not with End of speech pause at 1.5 s;
// the quiet after the sentence still ends it there, at the end of what was said.
func TestEndOfSpeechPauseLetsAPauseGoBy(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	sentence := [][]int16{voiceAt(10, 1500), voiceAt(10, 30), voiceAt(10, 1500)}

	ep := endpoint.New(endpointSettings())
	ended := false
	for _, p := range sentence {
		ended = ep.Feed(p) || ended
	}
	if !ended {
		t.Fatal("at the default 0.6 s, a 1 s pause did not end the turn")
	}

	if err := config.Set().Microphone().EndPause(15); err != nil {
		t.Fatal(err)
	}
	ep = endpoint.New(endpointSettings())
	for _, p := range sentence {
		if ep.Feed(p) {
			t.Fatal("at 1.5 s, a 1 s pause ended the turn")
		}
	}
	if !ep.Feed(voiceAt(15, 30)) {
		t.Fatal("at 1.5 s, 1.5 s of quiet after the sentence did not end the turn")
	}
	if got := ep.EndedAt(); got != 30*endpoint.Window {
		t.Errorf("ended at %d samples, want the end of the sentence at %d", got, 30*endpoint.Window)
	}
}
