package setup

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
)

// Record wake word sits in the takes section, says plainly what has to be set first, and once it can
// run offers the count and the gap with their ranges.
func TestRecordWakeWordSaysWhatToSetFirst(t *testing.T) {
	f, c := in(t)
	if err := config.Set().Security().Takes(true); err != nil {
		t.Fatal(err)
	}
	was := runBlocked
	t.Cleanup(func() { runBlocked = was })
	runBlocked = func() takes.RunBlock { return takes.RunNotWakesmith }
	body := get(f, "/setup?tab=voice", c).Body.String()
	if !strings.Contains(body, "<strong>Record wake word</strong>") || !strings.Contains(body, "Set Send wake clips to wakesmith first.") ||
		strings.Contains(body, `name="do" value="run"`) {
		t.Error("the section did not say to set wakesmith first, or offered the run anyway")
	}
	runBlocked = func() takes.RunBlock { return takes.RunReady }
	body = get(f, "/setup?tab=voice", c).Body.String()
	for _, want := range []string{`name="do" value="run"`, `name="count" type="number" min="5" max="50" value="20"`,
		`name="gap" type="number" min="2" max="10" value="3"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the form has no %q", want)
		}
	}
}

// Asking for a run takes the page's own token, hands the count and the gap on, and says why it was
// refused.
func TestRecordWakeWordAsksForTheRun(t *testing.T) {
	f, c := in(t)
	var asked [][4]any
	refuse := error(nil)
	was := armRun
	t.Cleanup(func() { armRun = was })
	armRun = func(count, gap, slot int, session string) error {
		asked = append(asked, [4]any{count, gap, slot, session})
		return refuse
	}
	run := url.Values{"do": {"run"}, "count": {"30"}, "gap": {"5"}}
	if w := postTakes(f, c, "not the token", run); w.Code != http.StatusForbidden || len(asked) != 0 {
		t.Errorf("asked with the wrong token: %d %v", w.Code, asked)
	}
	if w := postTakes(f, c, c.Value, run); w.Code != http.StatusSeeOther || len(asked) != 1 || asked[0] != [4]any{30, 5, -1, c.Value} {
		t.Errorf("asked %v (%d)", asked, w.Code)
	}
	// The wake word chosen on the page goes with it; a slot that is not a number is no choice.
	run.Set("slot", "1")
	if postTakes(f, c, c.Value, run); len(asked) != 2 || asked[1] != [4]any{30, 5, 1, c.Value} {
		t.Errorf("asked %v with slot 1", asked)
	}
	run.Set("slot", "x")
	if postTakes(f, c, c.Value, run); len(asked) != 3 || asked[2] != [4]any{30, 5, -1, c.Value} {
		t.Errorf("asked %v with slot x", asked)
	}
	run.Del("slot")
	refuse = errors.New("Set Send wake clips to wakesmith first")
	w := postTakes(f, c, c.Value, run)
	to, _ := url.Parse(w.Header().Get("Location"))
	if to.Query().Get("problem") != "Set Send wake clips to wakesmith first" || to.Fragment != "sec-takes" {
		t.Errorf("sent back to %s", to)
	}
}

// The state the page polls says which take a run is on and how many are on their way.
func TestTheTakesStateCountsARun(t *testing.T) {
	f, c := in(t)
	w := get(f, "/setup/takes/state", c)
	if body := w.Body.String(); !strings.Contains(body, `"take":0`) || !strings.Contains(body, `"sent":0`) {
		t.Errorf("state %s", body)
	}
}

// With two wake words loaded the form offers both, the one the next run is of chosen; with one there is
// nothing to choose and no choice.
func TestRecordWakeWordOffersTheLoadedWords(t *testing.T) {
	f, c := in(t)
	if err := config.Set().Security().Takes(true); err != nil {
		t.Fatal(err)
	}
	wasBlocked, wasWords, wasWord := runBlocked, runWords, runWord
	t.Cleanup(func() { runBlocked, runWords, runWord = wasBlocked, wasWords, wasWord })
	runBlocked = func() takes.RunBlock { return takes.RunReady }
	alfred := takes.SlotWord{Slot: 0, ID: "hey_alfred", Front: "tflm", Phrase: "Hey Alfred"}
	alexa := takes.SlotWord{Slot: 1, ID: "alexa", Front: "zserge", Phrase: "Alexa <b>"}
	words := []takes.SlotWord{alfred}
	runWords = func() []takes.SlotWord { return words }
	runWord = func() (takes.SlotWord, bool) { return alexa, true }
	if body := get(f, "/setup?tab=voice", c).Body.String(); strings.Contains(body, `name="slot"`) {
		t.Error("a choice of one word")
	}
	words = []takes.SlotWord{alfred, alexa}
	body := get(f, "/setup?tab=voice", c).Body.String()
	for _, want := range []string{`<select id="runslot" name="slot">`, `<option value="0">Hey Alfred</option>`,
		`<option value="1" selected>Alexa &lt;b&gt;</option>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the form has no %q", want)
		}
	}
}
