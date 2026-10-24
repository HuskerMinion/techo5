package setup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// someScores stands in for the engine, and says how it was last asked.
type someScores struct {
	sc          wakeword.Scores
	watched     []bool
	after, from []int64
}

func withScores(t *testing.T, sc wakeword.Scores) *someScores {
	t.Helper()
	s := &someScores{sc: sc}
	was := readScores
	t.Cleanup(func() { readScores = was })
	readScores = func(after uint64, from int64, watch bool) wakeword.Scores {
		s.watched = append(s.watched, watch)
		s.after, s.from = append(s.after, int64(after)), append(s.from, from)
		return s.sc
	}
	return s
}

// A word whose phrase is the model's own text, and one of each kind of event.
var shownScores = wakeword.Scores{
	Bucket: 100, From: 99, BucketMs: 250,
	Rows: []wakeword.ScoreRow{
		{Slot: 1, Role: "Wake word 1", ID: "hey_jarvis", Phrase: "Hey Jarvis", Ready: true, Threshold: 0.85, Cutoff: 0.75,
			Slack: true, Best: []float32{0.2, 0.6}, Settling: []bool{false, false}},
		{Slot: 104, Role: "Volume up word", ID: "turn_it_up", Phrase: `<script>alert(1)</script>`, Ready: true,
			Threshold: 0.8, Cutoff: 0.8},
	},
	Events: []wakeword.ScoreEvent{
		{Seq: 4, At: time.Date(2026, 10, 24, 10, 0, 0, 0, time.Local), Slot: 104, What: "near miss", ID: "turn_it_up", Peak: 0.62, Cutoff: 0.8},
		{Seq: 5, At: time.Date(2026, 10, 24, 10, 0, 5, 0, time.Local), Slot: 1, What: "detected", ID: "hey_jarvis", Peak: 0.97,
			Crossing: 0.91, Cutoff: 0.85},
	},
}

// The scores are behind the press like everything else on the page: they say when somebody spoke near
// the device.
func TestTheScoresNeedThePress(t *testing.T) {
	withScores(t, shownScores)
	f := build()
	f.Open()
	if code := get(f, "/setup/scores", nil).Code; code != http.StatusForbidden {
		t.Errorf("the scores with no session got %d, want them refused", code)
	}
	c := ask(t, f)
	if code := get(f, "/setup/scores", c).Code; code != http.StatusForbidden {
		t.Errorf("the scores while only waiting got %d, want them refused", code)
	}
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if code := get(f, "/setup/scores", c).Code; code != http.StatusOK {
		t.Errorf("the scores after the press got %d", code)
	}

	r := httptest.NewRequest(http.MethodPost, "/setup/scores", nil)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	f.serve(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("a POST to the scores got %d, want it refused", w.Code)
	}
}

// The poll is JSON the page's script can draw, never cached, and asked for as watching, which is what
// keeps the engine keeping the peaks. The words' own text goes through as text.
func TestTheScoresAreJSONThePageCanDraw(t *testing.T) {
	s := withScores(t, shownScores)
	f, c := in(t)
	w := get(f, "/setup/scores?after=3&from=99", c)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache control %q, want no-store", cc)
	}
	var got wakeword.Scores
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v: %s", err, w.Body.String())
	}
	if len(got.Rows) != 2 || got.Rows[1].Slot != 104 || got.Rows[1].Phrase != shownScores.Rows[1].Phrase {
		t.Errorf("rows %+v", got.Rows)
	}
	if len(s.watched) == 0 || !s.watched[len(s.watched)-1] {
		t.Errorf("the poll asked with watch %v, want true", s.watched)
	}
	if n := len(s.after); n == 0 || s.after[n-1] != 3 || s.from[n-1] != 99 {
		t.Errorf("the poll asked after %v from %v, want 3 and 99", s.after, s.from)
	}

	// A first poll, or one with nonsense in it, asks from the start.
	get(f, "/setup/scores?after=x&from=-", c)
	if n := len(s.after); s.after[n-1] != 0 || s.from[n-1] != 0 {
		t.Errorf("a poll with no numbers asked after %d from %d, want 0 and 0", s.after[n-1], s.from[n-1])
	}
}

// A view left watching must not hold the page open: it polls by itself, and the idle time is there to
// close the page when the person who opened it has walked away.
func TestWatchingTheScoresDoesNotKeepThePageOpen(t *testing.T) {
	withScores(t, shownScores)
	f, c := in(t)
	soon := time.Now().Add(time.Minute)
	f.mu.Lock()
	f.openUntil = soon
	f.mu.Unlock()

	for range 3 {
		get(f, "/setup/scores", c)
	}
	f.mu.Lock()
	held := f.openUntil
	f.mu.Unlock()
	if !held.Equal(soon) {
		t.Errorf("polling the scores put the closing time off by %v", held.Sub(soon))
	}

	get(f, "/setup", c)
	f.mu.Lock()
	held = f.openUntil
	f.mu.Unlock()
	if !held.After(soon) {
		t.Error("using the page itself no longer keeps it open")
	}
}

// Each event is worded as a person reads it, with the word named as its row is and the numbers the log has.
func TestEachEventIsWordedAsTheLogHasIt(t *testing.T) {
	rows := shownScores.Rows
	for _, c := range []struct {
		ev   wakeword.ScoreEvent
		want string
	}{
		{shownScores.Events[0], "Volume up word: near miss, peak 0.62 against 0.80"},
		{shownScores.Events[1], "Wake word 1: heard, peak 0.97, crossed 0.91 against 0.85"},
		{wakeword.ScoreEvent{Slot: 1, What: "unsure", Peak: 0.7, AskedFrom: 0.65, Cutoff: 0.85}, "unsure, peak 0.70, over the confirm threshold 0.65 and under the 0.85 that wakes"},
		{wakeword.ScoreEvent{Slot: 102, What: "ready", ID: "turn_it_down"}, "slot=102: turn_it_down ready"},
		{wakeword.ScoreEvent{Slot: 1, What: "dropped", ID: "hey_jarvis"}, "hey_jarvis taken out, its model failed"},
	} {
		if got := eventLine(c.ev, rows); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want it to say %q", c.ev.What, got, c.want)
		}
	}
}
