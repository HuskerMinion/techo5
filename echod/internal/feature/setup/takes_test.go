package setup

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// postTakes sends one of the section's buttons.
func postTakes(f *Feature, c *http.Cookie, token string, v url.Values) *httptest.ResponseRecorder {
	v.Set("token", token)
	r := httptest.NewRequest(http.MethodPost, "/setup/takes", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.serve(w, r)
	return w
}

// Asking for a recording is behind the press like everything else on the page, needs the page's own
// token, and is a POST: a link cannot ask for one.
func TestRecordingTakesNeedsThePressAndThePagesToken(t *testing.T) {
	f, c := in(t)
	record := url.Values{"do": {"record"}, "word": {"Hey Jarvis"}}
	if w := postTakes(f, nil, c.Value, record); w.Code != http.StatusForbidden {
		t.Errorf("asked with no session: %d", w.Code)
	}
	if w := postTakes(f, c, "not the token", record); w.Code != http.StatusForbidden {
		t.Errorf("asked with the wrong token: %d", w.Code)
	}
	// No cookie and no token is no session, however the two might compare.
	if w := postTakes(f, nil, "", record); w.Code != http.StatusForbidden {
		t.Errorf("asked with no session and no token: %d", w.Code)
	}
	if w := get(f, "/setup/takes?do=record&word=x", c); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("asked with a GET: %d", w.Code)
	}
	for _, path := range []string{"/setup/takes/state", "/setup/takes.zip", "/setup/takes/take?n=1"} {
		if w := get(f, path, nil); w.Code != http.StatusForbidden {
			t.Errorf("%s with no session: %d", path, w.Code)
		}
	}
}

// With the switch off the section says where it is, and asking anyway says the same.
func TestRecordingTakesSaysWhenItIsOff(t *testing.T) {
	f, c := in(t)
	if body := get(f, "/setup?tab=sound", c).Body.String(); !strings.Contains(body, "<legend>Record takes of a word</legend>") ||
		!strings.Contains(body, "It is off") || strings.Contains(body, `name="do" value="record"`) {
		t.Error("the section did not say it is off, or offered Record anyway")
	}
	w := postTakes(f, c, c.Value, url.Values{"do": {"record"}, "word": {"Hey Jarvis"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("asking: %d", w.Code)
	}
	to, _ := url.Parse(w.Header().Get("Location"))
	if !strings.Contains(to.Query().Get("problem"), "Privacy & Security") || to.Fragment != "takes" {
		t.Errorf("sent back to %s", to)
	}
	if err := config.Set().Security().Takes(true); err != nil {
		t.Fatal(err)
	}
	if body := get(f, "/setup?tab=sound", c).Body.String(); !strings.Contains(body, `name="do" value="record"`) {
		t.Error("the switch on did not offer Record")
	}
}

// Nothing kept is nothing to download, and the state is JSON that says so, never cached.
func TestNoTakesIsNothingToDownload(t *testing.T) {
	f, c := in(t)
	if w := get(f, "/setup/takes.zip", c); w.Code != http.StatusNotFound {
		t.Errorf("the zip with nothing kept: %d", w.Code)
	}
	if w := get(f, "/setup/takes/take?n=1", c); w.Code != http.StatusNotFound {
		t.Errorf("a take with nothing kept: %d", w.Code)
	}
	w := get(f, "/setup/takes/state", c)
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" ||
		!strings.Contains(w.Body.String(), `"phase":"idle"`) {
		t.Errorf("the state: %v %s", w.Header(), w.Body.String())
	}
}

// A tab left polling the recording's state is not somebody using the page.
func TestTheTakesStateDoesNotKeepThePageOpen(t *testing.T) {
	f, c := in(t)
	f.mu.Lock()
	until := f.openUntil.Add(-time.Minute)
	f.openUntil = until
	f.mu.Unlock()
	get(f, "/setup/takes/state", c)
	f.mu.Lock()
	kept := f.openUntil
	f.mu.Unlock()
	if !kept.Equal(until) {
		t.Error("polling the takes' state kept the page open")
	}
}

// The takes go when the page closes, however it closes.
func TestTheTakesGoWhenThePageCloses(t *testing.T) {
	var why []string
	was := discardTakes
	discardTakes = func(w string) { why = append(why, w) }
	t.Cleanup(func() { discardTakes = was })
	f, _ := in(t)
	why = nil // opening it threw away whatever an earlier page left
	f.Close()
	if len(why) != 1 || why[0] != "the setup page closed" {
		t.Errorf("closing the page threw away %v", why)
	}
}

// A press with no browser waiting goes to the recording waiting for it; a press that lets a browser in
// does not also start one. And voice is told the press was the page's while a recording waits.
func TestThePressStartsTakesOnlyWhenNoBrowserIsWaiting(t *testing.T) {
	pressed, took := 0, false
	wasPressed, wasTook := takesPressed, takesTookPress
	takesPressed = func() bool { pressed++; return true }
	takesTookPress = func() bool { return took }
	t.Cleanup(func() { takesPressed, takesTookPress = wasPressed, wasTook })

	f := build()
	f.Open()
	ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if pressed != 0 {
		t.Error("the press that let a browser in also went to the takes")
	}
	f.mu.Lock()
	f.took = time.Time{}
	f.mu.Unlock()
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if pressed != 1 {
		t.Errorf("a press with nobody waiting reached the takes %d times", pressed)
	}
	if f.TookPress() {
		t.Error("a press was said to be the page's with nothing waiting for it")
	}
	took = true
	if !f.TookPress() {
		t.Error("the press a recording waits for was left to voice")
	}
}

// A page opened again after it ran out, before Run's next look closed it, is a new page: the browsers
// let into the old one are not in it, and the takes the old one asked for are thrown away.
func TestAPageOpenedAgainIsANewPage(t *testing.T) {
	var why []string
	was := discardTakes
	discardTakes = func(w string) { why = append(why, w) }
	t.Cleanup(func() { discardTakes = was })
	f, c := in(t)
	f.mu.Lock()
	f.openUntil = time.Now().Add(-time.Second)
	f.mu.Unlock()
	why = nil
	f.Open()
	if w := get(f, "/setup/takes/state", c); w.Code != http.StatusForbidden {
		t.Errorf("a browser let into the page that ran out is still in: %d", w.Code)
	}
	if len(why) != 1 {
		t.Errorf("opening the page again threw away %v", why)
	}
}

// A recording asked for as the page closed is thrown away with it: the page's closing threw away what
// there was before the asking was made, and nothing it asked for outlives it.
func TestARecordingAskedForAsThePageClosedGoesWithIt(t *testing.T) {
	var why []string
	wasDiscard, wasArm := discardTakes, armTakes
	discardTakes = func(w string) { why = append(why, w) }
	t.Cleanup(func() { discardTakes, armTakes = wasDiscard, wasArm })
	f, c := in(t)
	armTakes = func(string, takes.Kind, string) error {
		f.Close() // between the page's look at the session and the asking
		return nil
	}
	why = nil
	if w := postTakes(f, c, c.Value, url.Values{"do": {"record"}, "word": {"Hey Jarvis"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("asking: %d", w.Code)
	}
	if len(why) != 2 {
		t.Errorf("the asking made as the page closed was not thrown away after it: %v", why)
	}
}

// Takes kept for a browser that has gone, forgotten or long idle, make way for another browser's
// recording, which nobody could otherwise ask for until the page closed. A browser still here keeps
// its own.
func TestTakesOfABrowserThatHasGoneMakeWay(t *testing.T) {
	var why []string
	wasDiscard, wasArm, wasOwner := discardTakes, armTakes, takesOwner
	discardTakes = func(w string) { why = append(why, w) }
	armTakes = func(string, takes.Kind, string) error { return nil }
	t.Cleanup(func() { discardTakes, armTakes, takesOwner = wasDiscard, wasArm, wasOwner })
	f, c := in(t)
	record := url.Values{"do": {"record"}, "word": {"Hey Jarvis"}}

	takesOwner = func() string { return "a browser long gone" }
	why = nil
	postTakes(f, c, c.Value, record)
	if len(why) != 1 || !strings.Contains(why[0], "gone") {
		t.Errorf("a gone browser's takes were not made way for: %v", why)
	}

	takesOwner = func() string { return c.Value }
	why = nil
	postTakes(f, c, c.Value, record)
	if len(why) != 0 {
		t.Errorf("a browser still here had its takes thrown away: %v", why)
	}
}

// A download that cannot finish fails as one: thrown away before anything went, it is a 404 with no
// file to save; part way, the connection is broken off rather than a broken zip kept as whole.
func TestADownloadThatCannotFinishFails(t *testing.T) {
	gone := errors.New("thrown away")
	w := httptest.NewRecorder()
	sendZip(w, "techo5-takes-word.zip", func(io.Writer) error { return gone })
	if w.Code != http.StatusNotFound || w.Header().Get("Content-Disposition") != "" {
		t.Errorf("thrown away before anything went: %d, %q", w.Code, w.Header().Get("Content-Disposition"))
	}

	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Errorf("thrown away part way: %v, want the connection broken off", r)
		}
	}()
	sendZip(httptest.NewRecorder(), "techo5-takes-word.zip", func(out io.Writer) error {
		out.Write([]byte("PK\x03\x04"))
		return gone
	})
}
