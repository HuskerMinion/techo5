package setup

import (
	"strings"
	"testing"
)

// The section works with no script: the table names each word, its slot as the log has it, its threshold
// and what it is judged against now, and the last events are listed, newest first, with a Refresh. Drawing
// it does not start the engine keeping the peaks; only the live view's poll does.
func TestTheScoresSectionWorksWithoutScript(t *testing.T) {
	s := withScores(t, shownScores)
	f, c := in(t)
	page := get(f, "/setup?tab=sound", c).Body.String()

	for _, want := range []string{
		"<legend>Wake word scores</legend>", `<td>slot=104</td>`, `<td class="scmodel">Hey Jarvis`,
		`<td class="scthr">0.85</td><td class="sccut">0.75 <span class="note">(lower while the speaker plays)</span></td>`,
		`<td class="scthr">0.80</td><td class="sccut">0.80</td>`,
		`<td class="sclate">0.60</td>`, "Volume up word: near miss, peak 0.62 against 0.80",
		`href="/setup?tab=sound&amp;scores=`, `#scores">Refresh</a>`, `data-after="5"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the section has no %q", want)
		}
	}
	if heard, near := strings.Index(page, "Wake word 1: heard"), strings.Index(page, "Volume up word: near miss"); heard < 0 || near < heard {
		t.Error("the events are not newest first")
	}
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Error("a model's phrase went onto the page unescaped")
	}
	for _, w := range s.watched {
		if w {
			t.Error("drawing the section asked the engine to start keeping the peaks")
		}
	}
}
