package setup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// connect presses Connect to Home Assistant as the page does, at the device's address host.
func connect(f *Feature, c *http.Cookie, token, base, host string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/setup/ha/connect",
		strings.NewReader(url.Values{"token": {token}, "url": {base}}.Encode()))
	r.Host = host
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.serve(w, r)
	return w
}

// signedInHA is a Home Assistant that takes one code, and the device's access kept in a file of the
// test's own, put back as it was after.
func signedInHA(t *testing.T) *httptest.Server {
	old := hass.Path
	hass.Path = filepath.Join(t.TempDir(), "hass.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch {
		case r.URL.Path == "/auth/token" && r.PostFormValue("code") == "the-code":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": 1800})
		case r.URL.Path == "/auth/revoke":
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(func() {
		_ = hass.Get().Disconnect(context.Background())
		srv.Close()
		hass.Path = old
	})
	return srv
}

// stateIn is the state Connect sent the browser to Home Assistant with.
func stateIn(t *testing.T, w *httptest.ResponseRecorder) (state string, to *url.URL) {
	t.Helper()
	if w.Code != http.StatusSeeOther {
		t.Fatalf("Connect answered %d: %s", w.Code, w.Body.String())
	}
	to, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return to.Query().Get("state"), to
}

// Connect is the page's like every other form: a browser not let in, or a form without the page's
// token, starts nothing.
func TestHAConnectIsThePages(t *testing.T) {
	f, c := in(t)
	if w := connect(f, nil, c.Value, "http://ha.test:8123", "dev.test:8181"); w.Code != http.StatusForbidden {
		t.Errorf("without the cookie: %d", w.Code)
	}
	if w := connect(f, c, "not-the-token", "http://ha.test:8123", "dev.test:8181"); w.Code != http.StatusForbidden {
		t.Errorf("without the page's token: %d", w.Code)
	}
	if w := connect(f, c, c.Value, "http://user:pass@ha.test:8123", "dev.test:8181"); w.Code != http.StatusSeeOther ||
		!strings.Contains(w.Header().Get("Location"), "problem=") {
		t.Errorf("an address with a login: %d %q", w.Code, w.Header().Get("Location"))
	}
}

// Connect sends the browser to Home Assistant's login with the page's own address as the client and
// the way back on that same address, and a state nobody can guess.
func TestHAConnectGoesToHomeAssistant(t *testing.T) {
	f, c := in(t)
	state, to := stateIn(t, connect(f, c, c.Value, "http://ha.test:8123/", "dev.test:8181"))
	q := to.Query()
	if to.Scheme+"://"+to.Host+to.Path != "http://ha.test:8123/auth/authorize" || q.Get("response_type") != "code" ||
		q.Get("client_id") != "http://dev.test:8181/" || q.Get("redirect_uri") != "http://dev.test:8181/setup/ha/back" {
		t.Fatalf("sent to %s", to)
	}
	if len(state) != 64 {
		t.Fatalf("state %q", state)
	}
}

// The way back signs the device in once: from Home Assistant's page, without the page's cookie, but
// only with a state Connect made, and only the once.
func TestHABackSignsInOnce(t *testing.T) {
	srv := signedInHA(t)
	f, c := in(t)
	state, _ := stateIn(t, connect(f, c, c.Value, srv.URL, "dev.test:8181"))

	if w := get(f, "/setup/ha/back?code=the-code&state=0123", nil); w.Code != http.StatusForbidden || hass.Get().SignedIn() {
		t.Fatalf("a state Connect did not make: %d, signed in %v", w.Code, hass.Get().SignedIn())
	}
	w := get(f, "/setup/ha/back?code=the-code&state="+state, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Connected") || !hass.Get().SignedIn() {
		t.Fatalf("back with the code: %d, signed in %v: %s", w.Code, hass.Get().SignedIn(), w.Body.String())
	}
	if w := get(f, "/setup/ha/back?code=the-code&state="+state, nil); w.Code != http.StatusForbidden {
		t.Fatalf("the same state again: %d", w.Code)
	}
	if body := get(f, "/setup?tab=network", c).Body.String(); !strings.Contains(body, "Connected to Home Assistant at") {
		t.Error("the Network tab does not say the device is connected")
	}
}

// A sign-in finished after the browser that started it was let go of is not taken.
func TestHABackNeedsTheBrowserStillIn(t *testing.T) {
	srv := signedInHA(t)
	f, c := in(t)
	state, _ := stateIn(t, connect(f, c, c.Value, srv.URL, "dev.test:8181"))
	f.leave(c.Value)
	if w := get(f, "/setup/ha/back?code=the-code&state="+state, nil); w.Code != http.StatusForbidden || hass.Get().SignedIn() {
		t.Fatalf("after the browser left: %d, signed in %v", w.Code, hass.Get().SignedIn())
	}
}

// Home Assistant's way back is only ever the page's own address: a Host that is not a plain host and
// port is turned away rather than handed to Home Assistant as the client.
func TestPageOrigin(t *testing.T) {
	for host, want := range map[string]string{
		"192.168.1.20:8181":  "http://192.168.1.20:8181",
		"echo-show.local":    "http://echo-show.local",
		"[fd00::5]:8181":     "http://[fd00::5]:8181",
		"evil.test/x?":       "",
		"user@dev.test:8181": "",
		"":                   "",
	} {
		r := httptest.NewRequest(http.MethodGet, "/setup", nil)
		r.Host = host
		got, ok := pageOrigin(r)
		if want == "" && ok || want != "" && got != want {
			t.Errorf("%q: %q %v, want %q", host, got, ok, want)
		}
	}
}
