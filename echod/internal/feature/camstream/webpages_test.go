package camstream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func pageConfig(t *testing.T) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().CamStream().Login("viewer", "secret12"); err != nil {
		t.Fatal(err)
	}
}

// askPage requests /camera.jpg from addr with a Digest login made from the nonce the device hands out.
func askPage(t *testing.T, addr, user, pass string) int {
	t.Helper()
	ok := func(w http.ResponseWriter, r *http.Request) {
		if PageAllowed(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	}
	r := httptest.NewRequest("GET", "/camera.jpg", nil)
	r.RemoteAddr = addr + ":5000"
	w := httptest.NewRecorder()
	ok(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no login: %d", w.Code)
	}
	m := regexp.MustCompile(`nonce="([^"]+)"`).FindStringSubmatch(w.Header().Get("WWW-Authenticate"))
	if m == nil {
		t.Fatalf("no nonce in %q", w.Header().Get("WWW-Authenticate"))
	}
	ha1 := md5hex(user + ":" + realm + ":" + pass)
	ha2 := md5hex("GET:/camera.jpg")
	resp := md5hex(ha1 + ":" + m[1] + ":" + ha2)
	r = httptest.NewRequest("GET", "/camera.jpg", nil)
	r.RemoteAddr = addr + ":5000"
	r.Header.Set("Authorization", fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="/camera.jpg", response="%s"`, user, realm, m[1], resp))
	w = httptest.NewRecorder()
	ok(w, r)
	return w.Code
}

func TestPageNeedsLogin(t *testing.T) {
	pageConfig(t)
	if c := askPage(t, "192.0.2.10", "viewer", "secret12"); c != http.StatusOK {
		t.Errorf("right login: %d", c)
	}
	if c := askPage(t, "192.0.2.11", "viewer", "wrong"); c != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", c)
	}
	if c := askPage(t, "192.0.2.12", "someone", "secret12"); c != http.StatusUnauthorized {
		t.Errorf("wrong user: %d", c)
	}
}

func TestPageNoPasswordNoLogin(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "fresh.json"))
	if c := askPage(t, "192.0.2.13", config.DefaultCamStreamUser, ""); c != http.StatusUnauthorized {
		t.Errorf("a device with no password let a page in: %d", c)
	}
}

func TestPageNonce(t *testing.T) {
	now := time.Now()
	n := pageNonce(now)
	if !nonceOK(n, now) {
		t.Error("fresh nonce refused")
	}
	if nonceOK(n, now.Add(nonceFor+time.Second)) {
		t.Error("old nonce taken")
	}
	if nonceOK(n[:len(n)-1]+"0", now) && n[len(n)-1] != '0' {
		t.Error("altered nonce taken")
	}
	if nonceOK("nonsense", now) {
		t.Error("made-up nonce taken")
	}
}

// An aged-out nonce of ours asks again with stale=true; a made-up one does not.
func TestPageStaleNonce(t *testing.T) {
	pageConfig(t)
	old := pageNonce(time.Now().Add(-nonceFor - time.Minute))
	for _, c := range []struct {
		nonce string
		stale bool
	}{{old, true}, {"5f00." + "abcd", false}} {
		r := httptest.NewRequest("GET", "/camera.jpg", nil)
		r.RemoteAddr = "192.0.2.20:5000"
		r.Header.Set("Authorization", fmt.Sprintf(`Digest username="viewer", realm="%s", nonce="%s", uri="/camera.jpg", response="0"`, realm, c.nonce))
		w := httptest.NewRecorder()
		if PageAllowed(w, r) {
			t.Fatal("let in with a bad nonce")
		}
		if got := regexp.MustCompile(`stale=true`).MatchString(w.Header().Get("WWW-Authenticate")); got != c.stale {
			t.Errorf("nonce %q: stale=%v, want %v", c.nonce, got, c.stale)
		}
	}
}

// The uri may come in the absolute form; another path does not do.
func TestPageURIForms(t *testing.T) {
	r := httptest.NewRequest("GET", "/camera.jpg?t=1", nil)
	for uri, want := range map[string]bool{
		"/camera.jpg?t=1":                      true,
		"http://192.0.2.5:8181/camera.jpg?t=1": true,
		"/camera.mjpeg":                        false,
		"http://192.0.2.5:8181/other":          false,
	} {
		if got := uriMatches(uri, r); got != want {
			t.Errorf("%q: %v, want %v", uri, got, want)
		}
	}
}

func TestPageWatchingLightsAndLingers(t *testing.T) {
	old := lingerFor
	lingerFor = 50 * time.Millisecond
	defer func() { lingerFor = old }()
	done := PageWatching()
	if !Watching() {
		t.Fatal("a page sending the camera is not watching")
	}
	done()
	done() // twice is once
	if !Watching() {
		t.Error("the mark went before the linger")
	}
	deadline := time.Now().Add(2 * time.Second)
	for Watching() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if Watching() {
		t.Error("still watching after the page was done")
	}
}
