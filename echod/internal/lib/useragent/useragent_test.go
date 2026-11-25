package useragent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

func TestStringNamesTheVersion(t *testing.T) {
	want := "TECHO5/" + layout.Version + " (+https://github.com/HuskerMinion/techo5)"
	if got := String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// A request without a User-Agent is sent with the device's; one that has its own keeps it; the
// caller's request is never changed.
func TestTransportSetsOnlyAMissingUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
	}))
	defer srv.Close()
	c := &http.Client{Transport: Transport(nil)}

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got != String() {
		t.Errorf("sent %q, want %q", got, String())
	}
	if req.Header.Get("User-Agent") != "" {
		t.Errorf("the caller's request was changed: %q", req.Header.Get("User-Agent"))
	}

	req, _ = http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("User-Agent", "own/1")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got != "own/1" {
		t.Errorf("a request's own User-Agent was replaced: %q", got)
	}
}
