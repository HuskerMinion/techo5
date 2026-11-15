package setup

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func postTo(f *Feature, path string, c *http.Cookie, v url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.serve(w, r)
	return w
}

func TestSharingFormsNeedThePage(t *testing.T) {
	f, c := in(t)
	for _, path := range []string{"/setup/share", "/setup/share-copy"} {
		if w := postTo(f, path, nil, url.Values{"on": {"1"}}); w.Code != http.StatusForbidden {
			t.Errorf("%s without being let in: %d", path, w.Code)
		}
		if w := postTo(f, path, c, url.Values{"token": {"wrong"}, "on": {"1"}}); w.Code != http.StatusForbidden {
			t.Errorf("%s from another page: %d", path, w.Code)
		}
	}
	if config.Get().Share.On {
		t.Fatal("sharing was turned on by a form that did not come from the page")
	}
}

func TestSharingTurnsOnAndKeeps(t *testing.T) {
	f, c := in(t)
	w := postTo(f, "/setup/share", c, url.Values{"token": {c.Value}, "on": {"1"}, "id": {"bass", "sendspin"},
		"keep_bass": {"1"}, "keep_sendspin": {"1"}})
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "saved=1") {
		t.Fatalf("%d %s", w.Code, w.Header().Get("Location"))
	}
	s := config.Get().Share
	if !s.On || !s.Kept("bass") {
		t.Fatalf("%+v", s)
	}
	if s.Kept("sendspin") {
		t.Error("a setting that is never shared was kept, as though it could be")
	}
	if body := get(f, "/setup?tab=system", c).Body.String(); !strings.Contains(body, "Shared settings") ||
		!strings.Contains(body, `name="on" value="1" checked`) {
		t.Error("General does not show sharing on")
	}

	// Off again: the switch, nothing else lost.
	postTo(f, "/setup/share", c, url.Values{"token": {c.Value}})
	if s := config.Get().Share; s.On || !s.Kept("bass") {
		t.Fatalf("turned off: %+v", s)
	}
}

func TestCopyNeedsSharingOn(t *testing.T) {
	f, c := in(t)
	w := postTo(f, "/setup/share-copy", c, url.Values{"token": {c.Value}, "copy": {"bass"}, "to": {"Office"}})
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "problem=") {
		t.Fatalf("%d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestSharingWithoutAHouseWordSaysSo(t *testing.T) {
	f, c := in(t)
	if body := get(f, "/setup?tab=system", c).Body.String(); !strings.Contains(body, "no house word set") {
		t.Error("no word for the house, and the page does not say it cannot share")
	}
}

func TestAnsweringALinkRequest(t *testing.T) {
	f, c := in(t)
	_ = config.Set().Share().On(true)
	_ = config.Set().Share().Update(func(s *config.Share) {
		config.AddRequest(s, "bass", "Office", time.Now())
		config.AddRequest(s, "treble", "Office", time.Now())
	})
	if body := get(f, "/setup?tab=system", c).Body.String(); !strings.Contains(body, "Office asks to link") {
		t.Error("the requests are not shown")
	}

	// Accepted: linked here, though Office cannot be reached to be told.
	w := postTo(f, "/setup/share-request", c, url.Values{"token": {c.Value}, "id": {"bass"}, "device": {"Office"}, "op": {"accept"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("%d", w.Code)
	}
	s := config.Get().Share
	if !s.LinkedWith("bass", "Office") || len(s.Requests) != 1 {
		t.Fatalf("accepting: %+v", s)
	}

	postTo(f, "/setup/share-request", c, url.Values{"token": {c.Value}, "id": {"treble"}, "device": {"Office"}, "op": {"decline"}})
	if s := config.Get().Share; s.LinkedWith("treble", "Office") || len(s.Requests) != 0 {
		t.Fatalf("declining: %+v", s)
	}

	// Nothing to answer that was not asked.
	w = postTo(f, "/setup/share-request", c, url.Values{"token": {c.Value}, "id": {"screen_theme"}, "device": {"Office"}, "op": {"accept"}})
	if !strings.Contains(w.Header().Get("Location"), "problem=") || config.Get().Share.LinkedWith("screen_theme", "Office") {
		t.Fatal("a link nobody asked for was accepted")
	}
}
