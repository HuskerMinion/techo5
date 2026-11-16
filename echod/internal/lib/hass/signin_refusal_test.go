package hass

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"
)

// Only Home Assistant's own refusal is one: a proxy's bare 403 while it restarts is not, so the
// device keeps its sign-in and tries again.
func TestABare403IsNotARefusal(t *testing.T) {
	for _, tc := range []struct {
		status  int
		body    string
		refused bool
	}{
		{http.StatusForbidden, "", false},
		{http.StatusUnauthorized, "<html>Bad gateway</html>", false},
		{http.StatusBadRequest, `{"error":"invalid_grant"}`, true},
		{http.StatusForbidden, `{"error":"access_denied"}`, true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c := &Client{http: srv.Client()}
		_, err := c.askToken(context.Background(), srv.URL, url.Values{"grant_type": {"refresh_token"}})
		srv.Close()
		if err == nil {
			t.Fatalf("%d %q: no error", tc.status, tc.body)
		}
		if got := errors.Is(err, errRefused); got != tc.refused {
			t.Errorf("%d %q: refused = %v, want %v", tc.status, tc.body, got, tc.refused)
		}
	}
}

// A renewal that is not answered leaves the token in hand to serve while it still has time.
func TestARenewalHiccupKeepsTheTokenInHand(t *testing.T) {
	was := Path
	Path = filepath.Join(t.TempDir(), "hass.json")
	defer func() { Path = was }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := &Client{http: srv.Client(), acc: access{URL: srv.URL, Token: "still-good", Refresh: "r",
		Expires: time.Now().Add(time.Minute)}}
	got, err := c.token(context.Background(), false)
	if err != nil || got.Token != "still-good" {
		t.Fatalf("token = %q, %v; want the one in hand", got.Token, err)
	}
	if !c.SignedIn() {
		t.Fatal("a hiccup signed the device out")
	}
}
