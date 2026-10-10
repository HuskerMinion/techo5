package hass

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
)

// An outside address is fetched through a dialer that refuses the device's own addresses, loopback
// among them, as the connection is made: the test server is on 127.0.0.1.
func TestFetchURLOutsideRefusesLoopback(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()
	c := newClient("http://ha:8123", "t")
	if _, err := c.FetchURL(srv.URL + "/x.jpg"); err == nil {
		t.Error("an address on loopback was fetched")
	}
	if hit {
		t.Error("the connection reached the server")
	}
}

// The check follows a redirect: an allowed host that sends the fetch to the device is refused there.
func TestFetchURLOutsideRefusesRedirectToDevice(t *testing.T) {
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the redirect was followed to the device")
	}))
	defer inner.Close()
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, inner.URL+"/x", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("fine"))
	}))
	defer outer.Close()
	// Both servers are on loopback, so the seam lets the first dial through and refuses the next.
	saved := netguard.Refused
	t.Cleanup(func() { netguard.Refused = saved })
	calls := 0
	netguard.Refused = func(net.IP) bool { calls++; return calls > 1 }
	c := newClient("http://ha:8123", "t")
	if _, err := c.FetchURL(outer.URL + "/start"); err == nil {
		t.Error("a redirect to the device was followed")
	}
}

// Home Assistant's own address is fetched with the token even though it is on the local network (and
// here on loopback, which the outside client refuses).
func TestFetchURLOwnHomeAssistantStillWorks(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("pic"))
	}))
	defer srv.Close()
	c := newClient(srv.URL, "tok")
	b, err := c.FetchURL(srv.URL + "/api/image/x.jpg")
	if err != nil || string(b) != "pic" || auth != "Bearer tok" {
		t.Errorf("got %q, %v, auth %q", b, err, auth)
	}
}

// A fetch whose context is canceled gives up at once, for Home Assistant's own addresses and for
// outside ones, and says so: the server sees the client go away.
func TestFetchURLContextStopsWhenCanceled(t *testing.T) {
	gone := make(chan struct{}, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		gone <- struct{}{}
	}))
	defer srv.Close()
	// A hand-made client has no guarded outside client, so the test server on loopback can be reached.
	for name, tc := range map[string]struct{ base, url string }{
		"own":     {srv.URL, "/api/image/x.jpg"},
		"outside": {"http://ha:8123", srv.URL + "/x.jpg"},
	} {
		c, url := &Client{acc: access{URL: tc.base, Token: "t"}, http: srv.Client()}, tc.url
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := c.FetchURLContext(ctx, url); done <- err }()
		time.Sleep(50 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s: error %v, want the cancellation", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: the fetch kept waiting after its context was canceled", name)
		}
		select {
		case <-gone:
		case <-time.After(2 * time.Second):
			t.Errorf("%s: the server never saw the client go away", name)
		}
	}
}
