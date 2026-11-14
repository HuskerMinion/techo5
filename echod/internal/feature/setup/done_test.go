package setup

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// done presses Done as a browser would, or as something that is not the page would.
func done(f *Feature, method string, c *http.Cookie, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/setup/done", strings.NewReader(url.Values{"token": {token}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.serve(w, r)
	return w
}

// Done is on every tab, as a form carrying the page's token, and not on the page a browser that is not
// in gets.
func TestEveryTabHasDone(t *testing.T) {
	f, c := in(t)
	for _, tb := range shownTabs() {
		body := get(f, "/setup?tab="+tb.id, c).Body.String()
		if !strings.Contains(body, `action="/setup/done"><input type="hidden" name="token" value="`+c.Value+`">`) ||
			!strings.Contains(body, `>Done</button>`) {
			t.Errorf("tab %s has no Done", tb.id)
		}
	}
	if strings.Contains(get(f, "/setup", nil).Body.String(), "/setup/done") {
		t.Error("the locked page offers Done")
	}
}

// Done ends the session of the browser that pressed it, and only that: it is sent to the locked page,
// which says the page is closed, and another browser that was let in is still in.
func TestDoneEndsThatBrowsersSession(t *testing.T) {
	f, c := in(t)
	other := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	if !f.letIn(other.Value) {
		t.Fatal("the second browser was not let in to begin with")
	}

	w := done(f, http.MethodPost, c, c.Value)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/setup?closed=1" {
		t.Fatalf("Done answered %d to %q", w.Code, w.Header().Get("Location"))
	}
	if f.letIn(c.Value) {
		t.Error("the browser that pressed Done is still in")
	}
	taken := false
	for _, ck := range w.Result().Cookies() {
		taken = taken || (ck.Name == cookieName && ck.Value == "" && ck.MaxAge < 0 && ck.HttpOnly)
	}
	if !taken {
		t.Error("Done left the browser its cookie")
	}
	if !f.On() || !f.letIn(other.Value) {
		t.Error("one browser's Done closed the page for the other")
	}

	// Where it is sent: the locked page, saying so, with the way to ask again and none of the settings.
	body := get(f, "/setup?closed=1", nil).Body.String()
	for _, want := range []string{"The page is closed.", `action="/setup/wait"`, "Ask to be let in"} {
		if !strings.Contains(body, want) {
			t.Errorf("after Done the page lacks %q: %s", want, first(body))
		}
	}
	if strings.Contains(body, `class="rail"`) {
		t.Error("the settings are still there after Done")
	}
	// The old cookie is nobody's now: with it, the settings are not shown and a save is refused.
	if strings.Contains(get(f, "/setup?tab=general", c).Body.String(), `class="rail"`) {
		t.Error("the cookie Done ended still opens the settings")
	}
	if w := done(f, http.MethodPost, c, c.Value); w.Code != http.StatusForbidden {
		t.Errorf("a second Done with the ended cookie got %d", w.Code)
	}
}

// Done is the page's to press: not without a session, not without the page's token, and not by a link.
func TestDoneNeedsTheSessionAndThePagesToken(t *testing.T) {
	f, c := in(t)
	if w := done(f, http.MethodPost, nil, c.Value); w.Code != http.StatusForbidden {
		t.Errorf("Done with no session got %d", w.Code)
	}
	if w := done(f, http.MethodPost, c, "wrong"); w.Code != http.StatusForbidden {
		t.Errorf("Done with somebody else's token got %d", w.Code)
	}
	if w := done(f, http.MethodPost, c, ""); w.Code != http.StatusForbidden {
		t.Errorf("Done with no token got %d", w.Code)
	}
	if w := done(f, http.MethodGet, c, c.Value); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Done by a link got %d", w.Code)
	}
	if !f.letIn(c.Value) {
		t.Error("a Done that was refused ended the session all the same")
	}
}
