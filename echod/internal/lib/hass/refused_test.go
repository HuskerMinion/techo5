package hass

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A token Home Assistant refuses is told apart from one it has not been asked about, and from one it
// takes, so a device whose access was revoked can say so (#706).
func TestARefusedTokenIsKnown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			http.Error(w, "401: Unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"message":"API running."}`))
	}))
	defer srv.Close()

	c := &Client{acc: access{URL: srv.URL, Token: "revoked"}, http: srv.Client()}
	if c.Refused() {
		t.Fatal("refused before Home Assistant was asked")
	}
	if _, err := c.Fetch("/api/"); err == nil {
		t.Fatal("a revoked token was taken")
	}
	if !c.Refused() {
		t.Fatal("a 401 did not count as refused")
	}

	c.mu.Lock()
	c.acc.Token = "good"
	c.mu.Unlock()
	if _, err := c.Fetch("/api/"); err != nil {
		t.Fatal(err)
	}
	if c.Refused() {
		t.Fatal("still refused after Home Assistant took the token")
	}
}
