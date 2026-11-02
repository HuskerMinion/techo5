package setup

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/activity"
)

func withPast(t *testing.T, turns []activity.Past) *[]activity.Past {
	t.Helper()
	was, wasClear := pastTurns, clearTurns
	t.Cleanup(func() { pastTurns, clearTurns = was, wasClear })
	pastTurns = func() []activity.Past { return turns }
	clearTurns = func() { turns = nil }
	return &turns
}

// The section lists the turns with their parts, escapes what was said, and says so when the words are
// withheld.
func TestHistorySectionEscapesAndWithholds(t *testing.T) {
	withPast(t, []activity.Past{
		{At: time.Now(), Slot: 1, Score: "0.91", Heard: "<script>alert(1)</script>", Reply: "ok & done", End: "completed", ListenMS: "1200", ThinkMS: "800"},
		{At: time.Now(), Slot: 0, End: "stopped", Withheld: true},
	})
	f, c := in(t)
	page := get(f, "/setup?tab=sound", c).Body.String()
	for _, want := range []string{"<legend>Recent voice requests</legend>", "&lt;script&gt;alert(1)&lt;/script&gt;", "ok &amp; done",
		"listen 1.2 seconds, think 0.8 seconds", "(not kept: Send voice text is off)", "<td>0.91</td>", "<td>stopped</td>"} {
		if !strings.Contains(page, want) {
			t.Errorf("the section has no %q", want)
		}
	}
	if strings.Contains(page, "<script>alert(1)") {
		t.Error("said text went onto the page unescaped")
	}
}

// Clear empties the history, and only for a page that was let in with its token.
func TestHistoryClear(t *testing.T) {
	turns := withPast(t, []activity.Past{{Slot: 1, Heard: "hello", End: "completed"}})
	f, c := in(t)
	if w := postTo(f, "/setup/history/clear", nil, url.Values{"token": {c.Value}}); w.Code != 403 || len(*turns) != 1 {
		t.Errorf("without the page: %d", w.Code)
	}
	if w := postTo(f, "/setup/history/clear", c, url.Values{"token": {"wrong"}}); w.Code != 403 || len(*turns) != 1 {
		t.Errorf("wrong token: %d", w.Code)
	}
	if w := postTo(f, "/setup/history/clear", c, url.Values{"token": {c.Value}}); w.Code != 303 || len(*turns) != 0 {
		t.Errorf("clear: %d, %d left", w.Code, len(*turns))
	}
}
