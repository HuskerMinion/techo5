package hass

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

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
