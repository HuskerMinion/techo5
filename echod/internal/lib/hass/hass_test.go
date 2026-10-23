package hass

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	defer func(was string) { Path = was }(Path)
	Path = filepath.Join(t.TempDir(), "hass.json")
	c := &Client{}
	if err := c.Set("\u200b\ufeff http://192.168.1.20:8123/ \n", " abc.def\u200b "); err != nil {
		t.Fatal(err)
	}
	if c.acc.URL != "http://192.168.1.20:8123" || c.acc.Token != "abc.def" {
		t.Errorf("kept %q and %q", c.acc.URL, c.acc.Token)
	}
	if err := c.Set("HTTPS://Home.example:8123/", "abc"); err != nil || c.acc.URL != "https://Home.example:8123" {
		t.Errorf("an upper-case scheme was kept as %q (%v)", c.acc.URL, err)
	}
	for _, bad := range []string{"192.168.1.20:8123", "homeassistant.local", "ftp://ha", "http://"} {
		if err := c.Set(bad, "abc"); err == nil {
			t.Errorf("%q was taken as an address", bad)
		}
	}
}

// A reply that is not a success from an address with a login in it says what went wrong without the
// login: the text is drawn on the screen and written to the log. The query and fragment, where a
// signed link keeps its token, go with it.
func TestFetchURLErrorHasNoLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := &Client{acc: access{URL: "http://ha:8123", Token: "t"}, http: srv.Client()}
	host := strings.TrimPrefix(srv.URL, "http://")
	_, err := c.FetchURL("http://user:secret@" + host + "/x.jpg?sig=abc#frag")
	if err == nil {
		t.Fatal("a 401 was taken as a picture")
	}
	for _, leak := range []string{"user", "secret", "sig", "abc", "frag"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q repeats %q", err, leak)
		}
	}
	for _, keep := range []string{"hass: GET http://" + host + "/x.jpg", "401 Unauthorized"} {
		if !strings.Contains(err.Error(), keep) {
			t.Errorf("error %q lost %q", err, keep)
		}
	}
}

// An address too broken to parse still loses its login and its query.
func TestWithoutLogin(t *testing.T) {
	for in, want := range map[string]string{
		"http://u:p@h/x?a=1#f":      "http://h/x",
		"http://u@h:81/x":           "http://h:81/x",
		"/api/x?token=1":            "/api/x",
		"http://u:p@h:bad/x?a=1":    "http://h:bad/x", // a bad port: not parsed, cut by hand
		"http://u:p@ss@h:bad/x?a=1": "http://h:bad/x",
		"http://h/x":                "http://h/x",
	} {
		if got := withoutLogin(in); got != want {
			t.Errorf("withoutLogin(%q) = %q, want %q", in, got, want)
		}
	}
}

// A forecast's dates are read in every form integrations send them. Deutscher Wetterdienst's come
// without a zone (2026-10-23T00:00:00), and read as nothing they all became the zero time, a Monday:
// the dashboard's row of days read Today, Mon, Mon, Mon, Mon.
func TestForecastDatesWithoutAZone(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string // the local date it is read as
	}{
		{"2026-10-23T00:00:00", "2026-10-23"},
		{"2026-10-23T00:00:00.000000", "2026-10-23"},
		{"2026-10-23 00:00:00", "2026-10-23"},
		{"2026-10-23", "2026-10-23"},
		{" 2026-10-23T00:00:00 ", "2026-10-23"},
		{"2026-10-23T12:00:00Z", time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).Local().Format("2006-01-02")},
		{"2026-10-23T00:00:00+02:00", time.Date(2026, 10, 22, 22, 0, 0, 0, time.UTC).Local().Format("2006-01-02")},
		{"2026-10-23 00:00:00+02:00", time.Date(2026, 10, 22, 22, 0, 0, 0, time.UTC).Local().Format("2006-01-02")},
	} {
		got, ok := forecastTime(tc.in)
		if !ok || got.Format("2006-01-02") != tc.want {
			t.Errorf("forecastTime(%q) = %v, %v; want %s", tc.in, got, ok, tc.want)
		}
	}
	if _, ok := forecastTime("tomorrow"); ok {
		t.Error("forecastTime read \"tomorrow\" as a date")
	}
}

// A forecast whose dates can't be read at all counts its days from today, one after another, rather
// than leaving every one of them a Monday.
func TestForecastDaysCountFromTodayWhenUnreadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"service_response":{"weather.home":{"forecast":[
			{"datetime":"2026-10-23T00:00:00","condition":"rainy","temperature":14,"templow":7.6},
			{"datetime":"2026-10-24T00:00:00","condition":"cloudy","temperature":12,"templow":6},
			{"datetime":"soon","condition":"sunny","temperature":15,"templow":5},
			{"condition":"sunny","temperature":16,"templow":5}]}}}`))
	}))
	defer srv.Close()
	c := &Client{acc: access{URL: srv.URL, Token: "secret"}, http: &http.Client{}}
	days, err := c.Forecast("weather.home")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now()
	want := []string{"2026-10-23", "2026-10-24", today.AddDate(0, 0, 2).Format("2006-01-02"), today.AddDate(0, 0, 3).Format("2006-01-02")}
	for i, d := range days {
		if got := d.When.Format("2006-01-02"); got != want[i] {
			t.Errorf("day %d is %s, want %s", i, got, want[i])
		}
	}
	if days[0].When.Weekday() == days[1].When.Weekday() {
		t.Errorf("the first two days are both %s", days[0].When.Weekday())
	}
}
