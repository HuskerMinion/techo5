package hass

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// recorder answers every request with a short body and keeps the last one, so a test can see the
// URL that went out and whether the token went with it.
type recorder struct {
	url  string
	auth string
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.url = req.URL.String()
	r.auth = req.Header.Get("Authorization")
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

// TestFetchURLToken pins down who gets the long-lived token: our own Home Assistant, and nobody
// else. A media source can hand back a URL on any host it likes, and that token opens the whole
// Home Assistant API.
func TestFetchURLToken(t *testing.T) {
	const base = "http://ha:8123"
	cases := []struct {
		name  string
		url   string
		want  string // the URL that should go out, empty when the fetch should not get that far
		token bool
	}{
		{"relative path", "/api/media/photo-1.jpg", "http://ha:8123/api/media/photo-1.jpg", true},
		{"the base itself", base, "http://ha:8123/", true},
		{"under the base", base + "/api/media/photo-1.jpg", "http://ha:8123/api/media/photo-1.jpg", true},
		{"with a query", base + "/api/media/photo-1.jpg?authSig=abc", "http://ha:8123/api/media/photo-1.jpg?authSig=abc", true},
		{"a third party", "https://jellyfin.example/Items/1/Images/Primary?tag=xyz", "https://jellyfin.example/Items/1/Images/Primary?tag=xyz", false},
		{"a host that only starts like ours", "http://ha:8123.evil.example/x", "", false},
		{"our host on another scheme", "https://ha:8123/x", "https://ha:8123/x", false},
		{"our name on another port", "http://ha:9123/x", "http://ha:9123/x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			c := &Client{acc: access{URL: base, Token: "secret"}, http: &http.Client{Transport: rec}}
			// http://ha:8123.evil.example/x does not even parse as a URL, so the fetch stops
			// before any request; what matters either way is that no token left the device.
			if _, err := c.FetchURL(tc.url); err != nil && tc.want != "" {
				t.Fatal(err)
			}
			if rec.url != tc.want {
				t.Errorf("fetched %s, want %s", rec.url, tc.want)
			}
			switch {
			case tc.token && rec.auth != "Bearer secret":
				t.Errorf("no token on our own Home Assistant: %q", rec.auth)
			case !tc.token && rec.auth != "":
				t.Errorf("token sent to %s: %q", tc.url, rec.auth)
			}
		})
	}
}

// TestOwnURL walks the boundaries on their own, including the bases and hostile URLs that a URL
// does parse — a name that merely starts with ours, and a path that merely starts with ours.
func TestOwnURL(t *testing.T) {
	cases := []struct {
		name, base, url, rest string
		ok                    bool
	}{
		{"no access at all", "", "http://ha:8123/x", "", false},
		{"the base itself", "http://ha:8123", "http://ha:8123", "/", true},
		{"under the base", "http://ha:8123", "http://ha:8123/api/x", "/api/x", true},
		{"a longer name", "http://ha", "http://ha.evil.example/x", "", false},
		{"a name ours is a prefix of", "http://ha:8123", "http://ha:8123.evil.example/x", "", false},
		{"behind a proxy path", "https://home.example/ha", "https://home.example/ha/api/x", "/api/x", true},
		{"a path ours is a prefix of", "https://home.example/ha", "https://home.example/hacked/x", "", false},
		{"the proxy path itself", "https://home.example/ha", "https://home.example/ha", "/", true},
		{"a credential that names our host", "http://ha:8123", "http://ha:8123@evil.example/x", "", false},
		// A port the scheme implies is the same host written out: spelling it either way must not
		// decide whether the token goes.
		{"the scheme's port spelled out", "http://ha", "http://ha:80/api/x", "/api/x", true},
		{"the scheme's port left out", "http://ha:80", "http://ha/api/x", "/api/x", true},
		{"https and its own port", "https://ha", "https://ha:443/api/x", "/api/x", true},
		{"the other scheme's port", "https://ha", "https://ha:80/api/x", "", false},
		{"a default port on another name", "http://ha", "http://ha.evil.example:80/x", "", false},
		{"not a URL at all", "http://ha:8123", "::nonsense", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rest, ok := ownURL(tc.base, tc.url)
			if ok != tc.ok || rest != tc.rest {
				t.Errorf("ownURL(%q, %q) = %q, %v; want %q, %v", tc.base, tc.url, rest, ok, tc.rest, tc.ok)
			}
		})
	}
}

// An address pasted with something invisible in front of it is kept without it, and one that is not an
// address at all is refused when it is set rather than failing every request after.
func TestTheAddressIsCleanedAndChecked(t *testing.T) {
	Path = filepath.Join(t.TempDir(), "hass.json")
	c := &Client{}
	if err := c.Set("\u200b\ufeff http://192.168.1.20:8123/ \n", " abc.def\u200b "); err != nil {
		t.Fatal(err)
	}
	if c.acc.URL != "http://192.168.1.20:8123" || c.acc.Token != "abc.def" {
		t.Errorf("kept %q and %q", c.acc.URL, c.acc.Token)
	}
	for _, bad := range []string{"192.168.1.20:8123", "homeassistant.local", "ftp://ha", "http://"} {
		if err := c.Set(bad, "abc"); err == nil {
			t.Errorf("%q was taken as an address", bad)
		}
	}
}
