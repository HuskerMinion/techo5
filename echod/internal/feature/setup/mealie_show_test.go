//go:build !dot && !spot

package setup

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The form as the page sends it carries the page's own token too, named "token": the Mealie token is
// its own field, and the page's is never taken for it.
func TestMealieSavesItsOwnToken(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	post := func(v url.Values) string {
		v.Set("token", "the-pages-own-token")
		v.Set("what", "mealie")
		return saveMealie(form(v))
	}
	if p := post(url.Values{"url": {"http://192.168.1.20:9000/"}, "mealietoken": {"mealie-secret"}}); p != "" {
		t.Fatal(p)
	}
	if m := config.Get().Mealie; m.URL != "http://192.168.1.20:9000" || m.Token != "mealie-secret" {
		t.Errorf("saved %+v", m)
	}
	// Saved again with the token field left empty: the saved token stays.
	if p := post(url.Values{"url": {"http://192.168.1.20:9000"}}); p != "" {
		t.Fatal(p)
	}
	if got := config.Get().Mealie.Token; got != "mealie-secret" {
		t.Errorf("an empty field changed the token to %q", got)
	}
	if p := post(url.Values{"url": {"http://192.168.1.20:9000"}, "notoken": {"yes"}}); p != "" || config.Get().Mealie.Token != "" {
		t.Errorf("remove left %q (%s)", config.Get().Mealie.Token, p)
	}
}

// A token is optional: a server that needs none is saved with the address alone, and its address may
// have a path when Mealie sits behind a proxy.
func TestMealieTokenIsOptional(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if p := saveMealie(form(url.Values{"url": {"https://home.example.com/mealie/"}})); p != "" {
		t.Fatal(p)
	}
	m := config.Get().Mealie
	if m.URL != "https://home.example.com/mealie" || m.Token != "" || !m.Set() {
		t.Errorf("saved %+v, set %v", m, m.Set())
	}
}

// What is not an address or a token is refused, and a refused form saves none of it.
func TestMealieRefusesWhatIsNotRight(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if p := saveMealie(form(url.Values{"url": {"http://192.168.1.20:9000"}, "mealietoken": {"kept"}})); p != "" {
		t.Fatal(p)
	}
	for _, bad := range []url.Values{
		{"url": {"http://user:pw@192.168.1.20:9000"}},
		{"url": {"ftp://192.168.1.20"}},
		{"url": {"192.168.1.20:9000"}},
		{"url": {"http://192.168.1.20:9000"}, "mealietoken": {"two\twords"}},
	} {
		if saveMealie(form(bad)) == "" {
			t.Errorf("%v was saved", bad)
		}
	}
	if m := config.Get().Mealie; m.URL != "http://192.168.1.20:9000" || m.Token != "kept" {
		t.Errorf("a refused form changed what was saved: %+v", m)
	}
}

// The saved token is never written into the page, only that there is one.
func TestMealieSectionNeverShowsTheToken(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if p := saveMealie(form(url.Values{"url": {"http://192.168.1.20:9000"}, "mealietoken": {"mealie-secret"}})); p != "" {
		t.Fatal(p)
	}
	w := httptest.NewRecorder()
	mealieSection(w, "the-pages-own-token")
	body := w.Body.String()
	if strings.Contains(body, "mealie-secret") {
		t.Error("the saved token is in the page")
	}
	if !strings.Contains(body, "saved: leave empty to keep it") {
		t.Error("the page does not say a token is saved")
	}
	if !strings.Contains(body, `value="http://192.168.1.20:9000"`) {
		t.Error("the page does not show the saved address")
	}
}
