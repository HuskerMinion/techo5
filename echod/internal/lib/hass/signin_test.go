package hass

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// signinHA is a Home Assistant that signs in with one code and renews with one refresh token, counting
// what it was asked.
type signinHA struct {
	srv                 *httptest.Server
	mu                  sync.Mutex
	access              string // the access token it takes now
	renewals, revokes   atomic.Int32
	refuse, expireFirst bool
	revoked             []string
	lastClient          string
}

func newSigninHA(t *testing.T) *signinHA {
	h := &signinHA{access: "access-1"}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/token":
			_ = r.ParseForm()
			h.mu.Lock()
			defer h.mu.Unlock()
			h.lastClient = r.PostFormValue("client_id")
			switch r.PostFormValue("grant_type") {
			case "authorization_code":
				if r.PostFormValue("code") != "the-code" {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
					return
				}
				in := 1800
				if h.expireFirst {
					in = 1 // runs out at once, inside renewAhead
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": h.access, "refresh_token": "refresh-1",
					"expires_in": in, "token_type": "Bearer"})
			case "refresh_token":
				if h.refuse || r.PostFormValue("refresh_token") != "refresh-1" {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				n := h.renewals.Add(1)
				time.Sleep(20 * time.Millisecond) // long enough for the other callers to queue behind it
				h.access = "access-" + string(rune('1'+n))
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": h.access, "expires_in": 1800})
			}
		case "/auth/revoke":
			_ = r.ParseForm()
			h.revokes.Add(1)
			h.mu.Lock()
			h.revoked = append(h.revoked, r.PostFormValue("token"))
			h.mu.Unlock()
		default:
			h.mu.Lock()
			ok := r.Header.Get("Authorization") == "Bearer "+h.access
			h.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"state":"on"}`))
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func signinClient(t *testing.T) *Client {
	old := Path
	Path = filepath.Join(t.TempDir(), "hass.json")
	t.Cleanup(func() { Path = old })
	return newClient("", "")
}

// A sign-in trades the code for the device's own tokens, keeps them, and the requests after carry the
// access token.
func TestSignInTradesTheCode(t *testing.T) {
	h := newSigninHA(t)
	c := signinClient(t)
	ctx := context.Background()
	if err := c.SignIn(ctx, h.srv.URL, "http://dev:8181/", "a-wrong-code"); err == nil {
		t.Fatal("a code Home Assistant refused signed the device in")
	}
	if c.Ready() {
		t.Fatal("a refused sign-in left access behind")
	}
	if err := c.SignIn(ctx, h.srv.URL, "http://dev:8181/", "the-code"); err != nil {
		t.Fatal(err)
	}
	if !c.SignedIn() || !c.Ready() || h.lastClient != "http://dev:8181/" {
		t.Fatalf("signed in %v, ready %v, client %q", c.SignedIn(), c.Ready(), h.lastClient)
	}
	if _, err := c.State("light.x"); err != nil {
		t.Fatalf("a request after signing in: %v", err)
	}
	// Kept: a client that reads the file again has the same sign-in.
	once, shared = sync.Once{}, nil
	t.Cleanup(func() { once, shared = sync.Once{}, nil })
	re := Get()
	if !re.SignedIn() || re.Token() != "access-1" {
		t.Fatalf("read back: signed in %v, token %q", re.SignedIn(), re.Token())
	}
}

// An access token about to run out is renewed once, however many callers find it so at the same
// time, and they all go on with the renewed one.
func TestSignInRenewsOnce(t *testing.T) {
	h := newSigninHA(t)
	h.expireFirst = true
	c := signinClient(t)
	if err := c.SignIn(context.Background(), h.srv.URL, "http://dev:8181/", "the-code"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.State("light.x")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := h.renewals.Load(); n != 1 {
		t.Fatalf("renewed %d times for eight callers", n)
	}
}

// A token Home Assistant refuses before its time is renewed and the request asked again, once.
func TestSignInRenewsAfterARefusal(t *testing.T) {
	h := newSigninHA(t)
	c := signinClient(t)
	if err := c.SignIn(context.Background(), h.srv.URL, "http://dev:8181/", "the-code"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.access = "rotated-elsewhere" // Home Assistant no longer takes access-1
	h.mu.Unlock()
	// The renewal hands out access-2; the server then takes that one.
	if _, err := c.State("light.x"); err != nil {
		t.Fatalf("after a refused token: %v", err)
	}
	if h.renewals.Load() != 1 {
		t.Fatalf("renewed %d times", h.renewals.Load())
	}
}

// A refresh token Home Assistant took back signs the device out: the access is gone, the page can say
// so, and the next request says why it failed.
func TestSignInTakenBack(t *testing.T) {
	h := newSigninHA(t)
	h.expireFirst = true
	c := signinClient(t)
	if err := c.SignIn(context.Background(), h.srv.URL, "http://dev:8181/", "the-code"); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.refuse = true
	h.mu.Unlock()
	if _, err := c.State("light.x"); err == nil || !strings.Contains(err.Error(), "connect it again") {
		t.Fatalf("a request with the sign-in taken back: %v", err)
	}
	if c.Ready() || c.SignedIn() || !c.SignedOut() || c.Address() == "" {
		t.Fatalf("ready %v, signed in %v, signed out %v, address %q", c.Ready(), c.SignedIn(), c.SignedOut(), c.Address())
	}
}

// Disconnect takes the sign-in back at Home Assistant and forgets it; a long-lived token given after a
// sign-in replaces it and takes it back too.
func TestSignInDisconnectRevokes(t *testing.T) {
	h := newSigninHA(t)
	c := signinClient(t)
	ctx := context.Background()
	if err := c.SignIn(ctx, h.srv.URL, "http://dev:8181/", "the-code"); err != nil {
		t.Fatal(err)
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Ready() || h.revokes.Load() != 1 || h.revoked[0] != "refresh-1" {
		t.Fatalf("ready %v, revokes %d %v", c.Ready(), h.revokes.Load(), h.revoked)
	}
	if err := c.SignIn(ctx, h.srv.URL, "http://dev:8181/", "the-code"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(h.srv.URL, "long-lived"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.revokes.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if h.revokes.Load() != 2 || c.SignedIn() {
		t.Fatalf("a long-lived token after a sign-in: revokes %d, signed in %v", h.revokes.Load(), c.SignedIn())
	}
}

// Only a plain Home Assistant address is signed in with: no login, query or fragment in it.
func TestCheckAddress(t *testing.T) {
	for raw, ok := range map[string]bool{
		"http://homeassistant.local:8123":  true,
		"https://ha.example:8123/":         true,
		"http://user:pass@ha:8123":         false,
		"http://ha:8123/?next=x":           false,
		"javascript:alert(1)":              false,
		"ftp://ha:8123":                    false,
		"http://192.168.1.10:8123#section": false,
	} {
		if _, err := CheckAddress(raw); (err == nil) != ok {
			t.Errorf("%q: accepted %v, want %v", raw, err == nil, ok)
		}
	}
}
