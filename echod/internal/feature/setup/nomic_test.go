//go:build !dot && !spot && !panel

package setup

import (
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// A device without microphones (the Skylight Calendar) has no voice to set up on this page: no voice
// assistant, realtime voice, listening after an answer, wake words or talk through cameras, and the
// Sound tab does not say Voice. What it plays is still there.
func TestNoVoiceSectionsWithoutAMicrophone(t *testing.T) {
	f, c := in(t)
	voice := []string{"<legend>Voice assistant</legend>", "<legend>Realtime voice</legend>",
		"<legend>Listening after an answer</legend>", "<legend>Wake words of your own</legend>",
		"<legend>Wake word scores</legend>",
		"<legend>Talk through cameras</legend>", "Sound &amp; Voice"}
	page := func() string {
		return get(f, "/setup?tab=sound", c).Body.String() + get(f, "/setup?tab=connections", c).Body.String()
	}
	with := page()
	for _, s := range voice {
		if !strings.Contains(with, s) {
			t.Errorf("with a microphone, no %s", s)
		}
	}
	was := layout.Board
	t.Cleanup(func() { layout.Board = was })
	layout.Board = "skylight"
	without := page()
	for _, s := range voice {
		if strings.Contains(without, s) {
			t.Errorf("without a microphone, %s is still on the page", s)
		}
	}
	for _, s := range []string{"<legend>Announcements</legend>", "<legend>Sounds of your own</legend>", "<h2>Sound</h2>"} {
		if !strings.Contains(without, s) {
			t.Errorf("without a microphone, %s went too", s)
		}
	}
}
