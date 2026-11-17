//go:build !dot && !spot && !panel

package setup

import (
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// A device without microphones (the Skylight Calendar) has no voice to set up on this page: no voice
// assistant, realtime voice, listening after an answer, wake words or talk through cameras, and the
// page has no Voice tab. What it plays is still there.
func TestNoVoiceSectionsWithoutAMicrophone(t *testing.T) {
	f, c := in(t)
	voice := []string{`<span class="ttl">Voice assistant</span>`, `<span class="ttl">Realtime voice</span>`,
		`<span class="ttl">Listening after an answer</span>`, `<span class="ttl">Wake words of your own</span>`,
		`<span class="ttl">Wake word scores</span>`,
		`<span class="ttl">Record takes of a word</span>`,
		`<span class="ttl">Talk through cameras</span>`, `href="/setup?tab=voice"`}
	page := func() string {
		return get(f, "/setup?tab=sound", c).Body.String() + get(f, "/setup?tab=voice", c).Body.String() +
			get(f, "/setup?tab=cameras", c).Body.String()
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
	for _, s := range []string{`<span class="ttl">Announcements</span>`, `<span class="ttl">Sounds of your own</span>`, "<h2>Sound</h2>"} {
		if !strings.Contains(without, s) {
			t.Errorf("without a microphone, %s went too", s)
		}
	}
}
