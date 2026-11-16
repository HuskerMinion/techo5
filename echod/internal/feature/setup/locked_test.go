package setup

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// A device with a screen is answered on its screen, and one without by its button: the page says the
// one this device has, asking and waiting.
func TestThePageSaysHowThisDeviceLetsABrowserIn(t *testing.T) {
	if by, now := letInWords(true); !strings.Contains(by, "Allow") || !strings.Contains(now, "screen") ||
		strings.Contains(by+now, "action button") {
		t.Errorf("a device with a screen says %q and %q", by, now)
	}
	if by, now := letInWords(false); !strings.Contains(by, "action button") || !strings.Contains(now, "action button") ||
		strings.Contains(by+now, "screen") {
		t.Errorf("a device with no screen says %q and %q", by, now)
	}
	if strings.Contains(reopenWords(true), "action button") || !strings.Contains(reopenWords(false), "holding the action button") {
		t.Error("how the page is opened again is said for the wrong device")
	}

	f := build()
	f.Open()
	wrong := "action button"
	if !hasScreen {
		wrong = "Allow"
	}
	body := get(f, "/setup", nil).Body.String()
	if !strings.Contains(body, letInBy) || strings.Contains(body, wrong) {
		t.Errorf("the page to ask from: %s", first(body))
	}
	c := ask(t, f)
	body = get(f, "/setup", c).Body.String()
	if !strings.Contains(body, letInNow) || strings.Contains(body, wrong) {
		t.Errorf("the page that waits: %s", first(body))
	}
}

// state is what the waiting page polls, as that browser.
func state(f *Feature, c *http.Cookie) string {
	return strings.TrimSpace(get(f, "/setup/state", c).Body.String())
}

// The waiting page stops waiting when the device does: the poll says the wait is over, and the page it
// goes to says nobody answered and offers to ask again, with nothing left polling.
func TestAWaitThatRanOutSaysSo(t *testing.T) {
	f := build()
	f.Open()
	c := ask(t, f)
	if got := state(f, c); got != `{"in":false,"waiting":true}` {
		t.Fatalf("while the device waits, the poll says %s", got)
	}
	body := get(f, "/setup?waited=1", c).Body.String()
	if strings.Contains(body, saidNoAnswer) || !strings.Contains(body, letInNow) || !strings.Contains(body, "waited=1") {
		t.Errorf("a browser still being asked about was told its wait was over: %s", first(body))
	}

	f.mu.Lock()
	f.waitingEnd = time.Now().Add(-time.Second) // the minute ran out with no press
	f.mu.Unlock()
	if got := state(f, c); got != `{"in":false,"waiting":false}` {
		t.Fatalf("after the wait ran out, the poll says %s", got)
	}
	body = get(f, "/setup?waited=1", c).Body.String()
	for _, want := range []string{saidNoAnswer, ">Ask again</button>", `action="/setup/wait"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page after a wait that ran out lacks %q: %s", want, first(body))
		}
	}
	if strings.Contains(body, "setInterval") || strings.Contains(body, letInNow) {
		t.Error("the page still waits for a press the device has stopped waiting for")
	}
	// Refused at the device is the same to the browser: the wait is over and it is not in.
	c = ask(t, f)
	f.Answer(false)
	if got := state(f, c); got != `{"in":false,"waiting":false}` {
		t.Errorf("after a refusal at the device, the poll says %s", got)
	}
	// And a press is still a press.
	c = ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if got := state(f, c); got != `{"in":true,"waiting":false}` {
		t.Errorf("after the press, the poll says %s", got)
	}
}

// The wait the poll reports is the browser's own. Another browser's wait is not this one's, and a
// browser the device is not asking about is not told to answer at the device, which would let the
// other one in.
func TestOnlyTheBrowserBeingAskedAboutIsToldToAnswer(t *testing.T) {
	f := build()
	f.Open()
	c := ask(t, f)
	if got := state(f, nil); got != `{"in":false,"waiting":false}` {
		t.Errorf("a browser that never asked is told %s", got)
	}
	other := &http.Cookie{Name: cookieName, Value: "walnut"}
	if got := state(f, other); got != `{"in":false,"waiting":false}` {
		t.Errorf("a browser with a cookie of its own making is told %s", got)
	}
	for _, who := range []*http.Cookie{nil, other} {
		body := get(f, "/setup", who).Body.String()
		if strings.Contains(body, letInNow) || strings.Contains(body, "setInterval") ||
			!strings.Contains(body, "Another browser is asking") {
			t.Errorf("a browser the device is not asking about got: %s", first(body))
		}
	}
	if body := get(f, "/setup", c).Body.String(); !strings.Contains(body, letInNow) {
		t.Error("the browser being asked about is not told to answer")
	}
}

// turnedAway is that an answer is a refusal of this code, as a page in the setup page's style with the
// way back on it.
func turnedAway(t *testing.T, what string, w *httptest.ResponseRecorder, code int, says string) {
	t.Helper()
	body := w.Body.String()
	if w.Code != code {
		t.Errorf("%s: got %d, want %d", what, w.Code, code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("%s: answered as %q, not a page", what, ct)
	}
	for _, want := range []string{"<!doctype html>", "<style>", `<a href="/setup">`, says} {
		if !strings.Contains(body, want) {
			t.Errorf("%s: the page lacks %q: %s", what, want, first(body))
		}
	}
	if strings.Contains(body, `class="rail"`) || strings.Contains(body, `name="token"`) {
		t.Errorf("%s: the refusal carries something of the settings", what)
	}
}

// A second browser asking, and asking too often, are still refused, and now with a page that says why.
func TestAskingRefusedIsAPage(t *testing.T) {
	f := build()
	f.Open()
	ask(t, f)
	w := httptest.NewRecorder()
	f.serve(w, httptest.NewRequest(http.MethodPost, "/setup/wait", nil))
	turnedAway(t, "a second browser", w, http.StatusConflict, "Another browser is already waiting")
	if len(w.Result().Cookies()) != 0 {
		t.Error("a browser that was refused was handed a cookie")
	}

	f.mu.Lock()
	f.shutUntil = time.Now().Add(time.Minute)
	f.mu.Unlock()
	w = httptest.NewRecorder()
	f.serve(w, httptest.NewRequest(http.MethodPost, "/setup/wait", nil))
	turnedAway(t, "asking too often", w, http.StatusTooManyRequests, "Too many tries")
	if len(w.Result().Cookies()) != 0 {
		t.Error("a browser that was shut out was handed a cookie")
	}
}

// A form sent by a browser that is not in, or not any more, is refused with a page, and saves nothing.
func TestAFormFromABrowserNotLetInIsRefusedWithAPage(t *testing.T) {
	f, c := in(t)
	send := func(who *http.Cookie) *httptest.ResponseRecorder {
		v := url.Values{"token": {c.Value}, "what": {"house"}, "tab": {"sound"}, "word": {"bluebird-fake-word"}}
		r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if who != nil {
			r.AddCookie(who)
		}
		w := httptest.NewRecorder()
		f.serve(w, r)
		return w
	}
	turnedAway(t, "a form with no session", send(nil), http.StatusForbidden, "Not let in")

	// The same browser once the page has let go of it.
	f.mu.Lock()
	delete(f.live, c.Value)
	f.mu.Unlock()
	w := send(c)
	turnedAway(t, "a form after the session ended", w, http.StatusForbidden, "Nothing was changed")
	if strings.Contains(w.Body.String(), "bluebird-fake-word") || strings.Contains(w.Body.String(), c.Value) {
		t.Error("the refusal repeats what was sent")
	}
	if got := config.Get().Home.HouseWord; got != "" {
		t.Errorf("a refused form saved the house word %q", got)
	}
	for _, path := range []string{"/setup/diagnostics.txt", "/setup/settings.json", "/setup/log"} {
		turnedAway(t, path, get(f, path, c), http.StatusForbidden, "Not let in")
	}
}

// A page that closed itself tells the browser that was using it so, and how it is opened again. To
// anything else it is the port's own 404, as a page that was never there.
func TestAClosedPageSaysSoToTheBrowserThatWasUsingIt(t *testing.T) {
	f, c := in(t)
	f.Close()

	come := func(method string, who *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/setup?tab=general&saved=%3Cb%3Ewalnut", nil)
		if who != nil {
			r.AddCookie(who)
		}
		w := httptest.NewRecorder()
		f.closedPage(w, r)
		return w
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		w := come(method, c)
		turnedAway(t, "the closed page by "+method, w, http.StatusNotFound, "The setup page is closed")
		if !strings.Contains(w.Body.String(), html.EscapeString(reopenBy)) || strings.Contains(w.Body.String(), "walnut") {
			t.Errorf("the closed page: %s", first(w.Body.String()))
		}
		if w.Header().Get("Connection") != "close" {
			t.Error("the closed page keeps the connection, with a body it has not read")
		}
	}
	for _, who := range []*http.Cookie{nil, {Name: cookieName, Value: ""}, {Name: "other", Value: "walnut"}} {
		w := come(http.MethodGet, who)
		if w.Code != http.StatusNotFound || w.Body.String() != "404 page not found\n" ||
			!strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
			t.Errorf("a stranger was told more than not found: %d %q", w.Code, w.Body.String())
		}
	}
	// Closed, nobody is in, whatever cookie they carry.
	if f.letIn(c.Value) {
		t.Error("the closed page's browser is still in")
	}
}

// What the locked page says happened comes from the page's own lines, never from the address.
func TestTheLockedPageShowsOnlyItsOwnWords(t *testing.T) {
	f := build()
	f.Open()
	body := get(f, "/setup?waited=%3Cscript%3Ewalnut&closed=%3Cb%3Ewalnut", nil).Body.String()
	if strings.Contains(body, "walnut") {
		t.Error("the locked page repeats what its address said")
	}
	if strings.Contains(get(f, "/setup", nil).Body.String(), `role="status"`) {
		t.Error("a browser that just arrived is told something happened")
	}
}
