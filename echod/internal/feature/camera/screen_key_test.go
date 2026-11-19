//go:build !dot

package camera

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
)

// /screen.png answers its key or a browser the setup page has let in, and nothing else: no key, a
// wrong one, or one cut short gets the refusal. The key lets the screen be read, never worked.
func TestTheScreenPageAsksForItsKey(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Security().ScreenKey("k3y-for-the-screen"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { web.Guard(nil) })
	web.Guard(func(r *http.Request) bool { return r.Header.Get("X-Let-In") != "" })

	for url, refused := range map[string]bool{
		"/screen.png":                                     true,
		"/screen.png?key=":                                true,
		"/screen.png?key=wrong":                           true,
		"/screen.png?key=k3y-for-the":                     true,
		"/screen.png?key=k3y-for-the-screen-and-more":     true,
		"/screen.png?key=k3y-for-the-screen":              false,
		"/screen.png?key=k3y-for-the-screen&theme=dusk":   true, // the key reads, it does not drive
		"/screen.png?key=k3y-for-the-screen&sheet=alarms": true,
	} {
		if got := screenRefused(httptest.NewRequest(http.MethodGet, url, nil)) != ""; got != refused {
			t.Errorf("%s: refused = %t, want %t", url, got, refused)
		}
	}

	// The setup page's browser needs no key, and may drive the screen as before.
	for _, url := range []string{"/screen.png", "/screen.png?theme=dusk&sheet=alarms"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		r.Header.Set("X-Let-In", "1")
		if why := screenRefused(r); why != "" {
			t.Errorf("%s from a browser the setup page let in: %s", url, why)
		}
	}
}

// A device with no key yet (the switch never on) lets nobody in by an empty key.
func TestNoKeyIsNotAKey(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if screenKeyOK("") {
		t.Error("an empty key opened the page on a device that has none")
	}
	if screenRefused(httptest.NewRequest(http.MethodGet, "/screen.png?key=", nil)) == "" {
		t.Error("/screen.png?key= was answered on a device with no key")
	}
}
