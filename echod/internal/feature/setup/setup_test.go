package setup

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// ask is a browser asking to be let in: it posts to /setup/wait and keeps the cookie it is given.
func ask(t *testing.T, f *Feature) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	f.serve(w, httptest.NewRequest(http.MethodPost, "/setup/wait", nil))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("asking to be let in: %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	t.Fatal("no cookie handed out to the browser that asked")
	return nil
}

func get(f *Feature, path string, c *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.serve(w, r)
	return w
}

// Nothing is readable until the button on the device has been pressed.
func TestNobodyIsLetInWithoutAPress(t *testing.T) {
	f := build()
	f.Open()

	if body := get(f, "/setup", nil).Body.String(); !strings.Contains(body, "press the") &&
		!strings.Contains(body, "Ask to be let in") {
		t.Errorf("a browser that has not asked is not told to press the button: %q", first(body))
	}

	c := ask(t, f)
	if body := get(f, "/setup", c).Body.String(); !strings.Contains(body, "Press the action button") {
		t.Errorf("a browser waiting for the press is not told to make it: %q", first(body))
	}
	if strings.Contains(get(f, "/setup", c).Body.String(), "Time zone") {
		t.Error("the settings are readable before anyone pressed anything")
	}

	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if body := get(f, "/setup", c).Body.String(); !strings.Contains(body, "Time zone") {
		t.Errorf("the press did not let the browser in: %q", first(body))
	}
}

// A press answers the browser that was already waiting. A second browser has to ask again, so that
// whoever presses knows which one they are letting in.
func TestOnlyOneBrowserWaitsAtATime(t *testing.T) {
	f := build()
	f.Open()
	first := ask(t, f)

	w := httptest.NewRecorder()
	f.serve(w, httptest.NewRequest(http.MethodPost, "/setup/wait", nil))
	if w.Code != http.StatusConflict {
		t.Errorf("a second browser asking got %d, want a refusal while another is waiting", w.Code)
	}

	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if !f.letIn(first.Value) {
		t.Error("the press did not let in the browser that was waiting")
	}
}

// A press with nobody waiting lets nobody in, and cannot be saved up for the next browser to ask.
func TestAPressIsNotSavedUp(t *testing.T) {
	f := build()
	f.Open()
	if f.press() {
		t.Error("a press with nobody waiting let somebody in")
	}
	c := ask(t, f)
	if f.letIn(c.Value) {
		t.Error("the browser that asked afterwards was let in by a press made before it asked")
	}
}

// Closing the page forgets every session: coming back means pressing again.
func TestClosingForgetsWhoWasLetIn(t *testing.T) {
	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if !f.letIn(c.Value) {
		t.Fatal("not let in to begin with")
	}
	f.Close()
	if f.letIn(c.Value) {
		t.Error("a session outlived the page being closed")
	}
	if f.On() {
		t.Error("still on after being closed")
	}
}

// The page closes itself when it is left alone, and asking for something keeps it open.
func TestItClosesWhenLeftAloneAndTypingKeepsItOpen(t *testing.T) {
	f := build()
	f.Open()

	f.mu.Lock()
	f.openUntil = time.Now().Add(-time.Second) // as if nothing had been asked of it for the idle time
	f.mu.Unlock()
	if f.On() {
		t.Error("still open after being left alone")
	}

	f.Open()
	f.mu.Lock()
	f.openUntil = time.Now().Add(time.Second)
	f.mu.Unlock()
	f.used()
	f.mu.Lock()
	left := time.Until(f.openUntil)
	f.mu.Unlock()
	if left < idle-time.Minute {
		t.Errorf("using the page left %v on it, want the idle time back at the start", left.Round(time.Second))
	}
}

// A save has to carry the token from the page it came from, so another site cannot post here with a
// browser's cookie.
func TestASaveNeedsThePagesOwnToken(t *testing.T) {
	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})

	post := func(body string) int {
		r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(c)
		w := httptest.NewRecorder()
		f.serve(w, r)
		return w.Code
	}
	if code := post("what=timezone&zone=UTC"); code != http.StatusForbidden {
		t.Errorf("a save with no token got %d, want it refused", code)
	}
	if code := post("token=wrong&what=timezone&zone=UTC"); code != http.StatusForbidden {
		t.Errorf("a save with somebody else's token got %d, want it refused", code)
	}
}

// Holding the action button opens the page, which is the only way in on a device with no screen.
func TestHoldingTheActionButtonOpensIt(t *testing.T) {
	f := build()
	if f.On() {
		t.Fatal("open before anyone asked")
	}
	f.button(buttons.Event{Name: buttons.Mute, Kind: buttons.Hold})
	if f.On() {
		t.Error("another button opened it")
	}
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Hold})
	if !f.On() {
		t.Error("holding the action button did not open it")
	}
}

func first(s string) string {
	if i := strings.Index(s, "<h1>"); i >= 0 {
		s = s[i:]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
