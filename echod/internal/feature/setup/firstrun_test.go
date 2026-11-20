package setup

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// firstRunIn is a browser let in on a device with first run to do, and what first run reads made up.
func firstRunIn(t *testing.T) (*Feature, *http.Cookie, *int) {
	t.Helper()
	f, c := in(t)
	if err := config.Set().FirstRun().Settle(false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set().FirstRun().Finish() }) // the next test's config may be this one
	firstrun.ResetForTests()
	opened := 0
	ws, hc, k, au, ao, ch, cu := firstrun.WifiStatus, firstrun.HomeConnected, firstrun.Key, firstrun.AdoptUntil,
		firstrun.OpenAdoption, firstrun.Chime, firstrun.CheckUpdates
	t.Cleanup(func() {
		// The updates step's look runs on its own and reads CheckUpdates: waited for before it is put back,
		// or a look the scheduler has not got to yet races the put back and calls the real one.
		firstrun.ResetForTests()
		firstrun.WifiStatus, firstrun.HomeConnected, firstrun.Key, firstrun.AdoptUntil = ws, hc, k, au
		firstrun.OpenAdoption, firstrun.Chime, firstrun.CheckUpdates = ao, ch, cu
	})
	firstrun.WifiStatus = func() wifi.Status {
		return wifi.Status{Connected: true, SSID: "Example Network", Address: "192.168.1.20"}
	}
	firstrun.HomeConnected = func() bool { return false }
	firstrun.Key = func() string { return "Zm9yIHRoZSBmaXJzdCBydW4gdGVzdHMgb25seSEhIQ==" }
	firstrun.AdoptUntil = func() time.Time { return time.Time{} }
	firstrun.OpenAdoption = func() (time.Time, error) { opened++; return time.Now().Add(15 * time.Minute), nil }
	firstrun.Chime = func() {}
	firstrun.CheckUpdates = func() {}
	return f, c, &opened
}

func firstRunPost(t *testing.T, f *Feature, c *http.Cookie, v url.Values) string {
	t.Helper()
	v.Set("token", c.Value)
	r := httptest.NewRequest(http.MethodPost, firstRunPath, strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	f.serve(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("posting %v: %d %s", v, w.Code, w.Body.String())
	}
	return w.Header().Get("Location")
}

// The page opens on first run while it is to do, walks its steps a form at a time, and is the usual
// page once Finish is pressed.
func TestTheSetupPageWalksFirstRun(t *testing.T) {
	f, c, opened := firstRunIn(t)
	if to := get(f, "/setup", c).Header().Get("Location"); to != firstRunPath {
		t.Fatalf("the page sent first run to %q", to)
	}
	titles := map[string]string{
		firstrun.Hello: "Hello", firstrun.Language: "Language and clock", firstrun.Wifi: "Wi-Fi", firstrun.Zone: "Time zone", firstrun.Name: "Name and room",
		firstrun.Home: "Home Assistant", firstrun.Updates: "Updates", firstrun.Sound: "Sound check",
	}
	for _, s := range firstrun.Steps()[:len(firstrun.Steps())-1] {
		body := get(f, firstRunPath, c).Body.String()
		if dir := os.Getenv("SETUP_PREVIEW"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, "setup-firstrun-"+map[bool]string{true: "hello", false: s}[s == ""]+".html"), []byte(body), 0o644)
		}
		if !strings.Contains(body, "<h2>"+titles[s]+"</h2>") {
			t.Fatalf("step %q: no %q heading in %s", s, titles[s], body)
		}
		if s == firstrun.Home {
			firstRunPost(t, f, c, url.Values{"what": {"adopt"}})
			if *opened != 1 {
				t.Fatal("Add it did not open the window")
			}
			firstRunPost(t, f, c, url.Values{"what": {"key"}})
			body := get(f, firstRunPath+"?key=1", c).Body.String()
			if !strings.Contains(body, "data:image/png;base64,") {
				t.Fatal("the key's QR code is not on the page")
			}
			if dir := os.Getenv("SETUP_PREVIEW"); dir != "" {
				_ = os.WriteFile(filepath.Join(dir, "setup-firstrun-home-key.html"), []byte(body), 0o644)
			}
		}
		if s == firstrun.Name {
			firstRunPost(t, f, c, url.Values{"what": {"room"}, "room": {"Other"}, "other": {"Sunroom"}})
			if config.Get().Home.Area != "Sunroom" {
				t.Fatalf("the room: %q", config.Get().Home.Area)
			}
		}
		firstRunPost(t, f, c, url.Values{"what": {"next"}})
	}
	if firstrun.Step() != firstrun.Done {
		t.Fatalf("after the steps: %q", firstrun.Step())
	}
	if to := firstRunPost(t, f, c, url.Values{"what": {"finish"}}); to != "/setup" || firstrun.Pending() {
		t.Fatalf("Finish: sent to %q, pending %v", to, firstrun.Pending())
	}
	if w := get(f, "/setup", c); w.Code != http.StatusOK {
		t.Fatalf("the page after first run: %d", w.Code)
	}
}

// A one-time code lets one browser in with no press, while first run is to do, once.
func TestTheOneTimeCode(t *testing.T) {
	config.Use(t.TempDir() + "/state.json")
	f := build()
	if _, ok := f.MintCode(); ok {
		t.Fatal("a code was minted with first run done")
	}
	if err := config.Set().FirstRun().Settle(false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set().FirstRun().Finish() }) // the next test's config may be this one
	code, ok := f.MintCode()
	if !ok || code == "" {
		t.Fatal("no code with first run to do")
	}
	w := get(f, "/setup?code=wrong", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Ask to be let in") {
		t.Fatal("a wrong code was let in")
	}
	w = get(f, "/setup?code="+url.QueryEscape(code), nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != firstRunPath {
		t.Fatalf("the code: %d to %q", w.Code, w.Header().Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}
	if cookie == nil || !f.letIn(cookie.Value) {
		t.Fatal("the code's browser was not let in")
	}
	if w := get(f, "/setup?code="+url.QueryEscape(code), nil); w.Code != http.StatusOK {
		t.Fatal("the same code worked twice")
	}
	code, _ = f.MintCode()
	codes.mu.Lock()
	codes.until = time.Now().Add(-time.Second)
	codes.mu.Unlock()
	if takeCode(code) {
		t.Fatal("an expired code worked")
	}
}

// During first run, with nothing connected, a press of the action button is setup's.
func TestAPressDuringFirstRunIsSetups(t *testing.T) {
	config.Use(t.TempDir() + "/state.json")
	hc := firstrun.HomeConnected
	t.Cleanup(func() { firstrun.HomeConnected = hc })
	firstrun.HomeConnected = func() bool { return false }
	f := build()
	if f.TookPress() {
		t.Fatal("a press was setup's with first run done")
	}
	if err := config.Set().FirstRun().Settle(false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set().FirstRun().Finish() }) // the next test's config may be this one
	if !f.TookPress() {
		t.Fatal("a press during first run was not setup's")
	}
	firstrun.HomeConnected = func() bool { return true }
	if f.TookPress() {
		t.Fatal("a press was setup's with Home Assistant connected")
	}
}

// A press during first run opens the page only once the device is on a network; offline it is left to
// Improv, which hears the press itself.
func TestAFirstRunPressOpensThePageOnlyOnline(t *testing.T) {
	config.Use(t.TempDir() + "/state.json")
	hc, on := firstrun.HomeConnected, online
	t.Cleanup(func() { firstrun.HomeConnected, online = hc, on })
	firstrun.HomeConnected = func() bool { return false }
	if err := config.Set().FirstRun().Settle(false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set().FirstRun().Finish() })
	f := build()
	up := false
	online = func() bool { return up }
	f.openWhenOnline()
	if f.On() {
		t.Fatal("the page opened with no network")
	}
	up = true
	f.openWhenOnline()
	if !f.On() {
		t.Fatal("the page did not open on the network")
	}
}

// With no key, adding it in Home Assistant is what the step says first, with Skip for now under it;
// once Home Assistant has it, the page says so.
func TestTheHomeAssistantStepWithNoKey(t *testing.T) {
	f, c, _ := firstRunIn(t)
	firstrun.Go(firstrun.Home)
	firstrun.Key = func() string { return "" }
	body := get(f, firstRunPath, c).Body.String()
	if !strings.Contains(body, `<div class="banner ok"><strong>Add it in Home Assistant now:`) ||
		!strings.Contains(body, ">Skip for now<") || strings.Contains(body, "Use it on its own") {
		t.Fatalf("the step with no key: %s", body)
	}
	firstrun.HomeConnected = func() bool { return true }
	if body := get(f, firstRunPath, c).Body.String(); !strings.Contains(body, "Home Assistant has added it.") {
		t.Fatalf("once added: %s", body)
	}
}

// The language step's form shows the language and clock as they are, and Next saves what was chosen
// and goes on; an unchanged language is left as it was, so a screen matching every language's words
// keeps doing so.
func TestTheSetupPagesLanguageStep(t *testing.T) {
	if !slices.Contains(firstrun.Steps(), firstrun.Language) {
		t.Skip("no screen: no language step")
	}
	f, c, _ := firstRunIn(t)
	if err := config.Set().FirstRun().Step(firstrun.Language); err != nil {
		t.Fatal(err)
	}
	body := get(f, firstRunPath, c).Body.String()
	for _, want := range []string{`<option value="en" selected>English</option>`, `id="clock12" value="12" checked`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the form has no %q: %s", want, body)
		}
	}
	firstRunPost(t, f, c, url.Values{"what": {"language"}, "language": {"en"}, "clock": {"12"}})
	if s := config.Get().Screen; s.Language != "" || s.Clock24 || firstrun.Step() != firstrun.After(firstrun.Language) {
		t.Fatalf("Next unchanged: language %q, 24 hours %v, step %q", s.Language, s.Clock24, firstrun.Step())
	}
	if err := config.Set().FirstRun().Step(firstrun.Language); err != nil {
		t.Fatal(err)
	}
	firstRunPost(t, f, c, url.Values{"what": {"language"}, "language": {"de"}, "clock": {"24"}})
	if s := config.Get().Screen; s.Language != "de" || !s.Clock24 || firstrun.Step() != firstrun.After(firstrun.Language) {
		t.Fatalf("Deutsch, 24 hours: language %q, 24 hours %v, step %q", s.Language, s.Clock24, firstrun.Step())
	}
	if err := config.Set().FirstRun().Step(firstrun.Language); err != nil {
		t.Fatal(err)
	}
	if to := firstRunPost(t, f, c, url.Values{"what": {"language"}, "language": {"xx"}, "clock": {"24"}}); !strings.Contains(to, "problem=") || firstrun.Step() != firstrun.Language {
		t.Fatalf("an unknown language went to %q, step %q", to, firstrun.Step())
	}
}

// Back is on every counted step but the first, walks the steps the other way, and does nothing where
// it is not offered.
func TestTheSetupPageGoesBack(t *testing.T) {
	f, c, _ := firstRunIn(t)
	steps := firstrun.Steps()
	backForm := `name="what" value="back"`
	for i, s := range steps {
		if err := config.Set().FirstRun().Step(s); err != nil {
			t.Fatal(err)
		}
		body := get(f, firstRunPath, c).Body.String()
		if want := firstrun.HasBack(s); strings.Contains(body, backForm) != want {
			t.Errorf("step %q: Back on the page %v, want %v", s, !want, want)
		}
		firstRunPost(t, f, c, url.Values{"what": {"back"}})
		want := s
		if firstrun.HasBack(s) {
			want = steps[i-1]
		}
		if got := firstrun.Step(); got != want {
			t.Errorf("Back on %q went to %q, want %q", s, got, want)
		}
	}
}
