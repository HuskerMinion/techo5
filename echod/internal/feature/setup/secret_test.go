package setup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The field for a secret: always empty, never one a password manager signs in with, a Show box that is
// hidden until the script brings it out, and whether one is saved said the same way everywhere.
func TestSecretField(t *testing.T) {
	saved := secretField("rtkey", "Key", "key", true, "nokey")
	for _, want := range []string{`<label for="rtkey">Key</label>`, `id="rtkey" name="key" type="password" value="" autocomplete="new-password"`,
		`<label class="show" hidden><input type="checkbox" data-show="rtkey"> Show</label>`, secretSaved,
		`<input type="checkbox" name="nokey" value="yes"> Remove the saved one`} {
		if !strings.Contains(saved, want) {
			t.Errorf("a saved secret's field lacks %q:\n%s", want, saved)
		}
	}
	none := secretField("rtkey", "Key", "key", false, "nokey")
	if !strings.Contains(none, secretNone) || strings.Contains(none, secretSaved) || strings.Contains(none, "nokey") {
		t.Errorf("with none saved the field says:\n%s", none)
	}
	if kept := secretField("k", "Key", "key", true, ""); strings.Contains(kept, "Remove") {
		t.Error("a secret its saver cannot remove offers to remove it")
	}
	// The Show box posts nothing: it has no name, so no saver ever sees it.
	if strings.Contains(saved, `data-show="rtkey" name=`) || strings.Count(saved, "name=") != 2 {
		t.Errorf("the field posts more than the secret and its remove box:\n%s", saved)
	}
	if got := secretField(`a"b`, `<i>Key</i>`, `k"k`, true, `r"r`); strings.Contains(got, `a"b`) || strings.Contains(got, "<i>") ||
		strings.Contains(got, `k"k`) || strings.Contains(got, `r"r`) {
		t.Errorf("the field's own words are not escaped:\n%s", got)
	}
	once := onceField("tskey", "Auth key", "key", `placeholder="tskey-auth-..."`)
	if !strings.Contains(once, `type="password" value="" autocomplete="new-password" placeholder="tskey-auth-..."`) ||
		!strings.Contains(once, `data-show="tskey"`) || strings.Contains(once, "saved") {
		t.Errorf("a secret used once is drawn as:\n%s", once)
	}
}

// The secrets every section holds, saved, for the tests that draw the sections: each an obviously made
// up word that appears nowhere else. Nothing here answers on the network: the servers named are a port
// on this machine that nothing listens on.
var fakeSecrets = map[string]string{
	"the voice assistant's key":     "walnut-fake-brain-key",
	"realtime voice's key":          "walnut-fake-realtime-key",
	"realtime voice's token":        "walnut-fake-mcp-token",
	"Music Assistant's token":       "walnut-fake-music-token",
	"the Reolink password":          "walnut-fake-recorder-pass",
	"the cameras' talk password":    "walnut-fake-talk-pass",
	"the camera stream's password":  "walnut-fake-stream-pass",
	"the dashboard server's key":    "walnut-fake-dash-key",
	"the OBS password":              "walnut-fake-obs-pass",
	"the Immich key":                "walnut-fake-immich-key",
	"the go2rtc address's password": "walnut-fake-rtc-pass",
}

func saveFakeSecrets(t *testing.T) {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	nowhere := "http://127.0.0.1:9"
	music, talk := fakeSecrets["Music Assistant's token"], fakeSecrets["the cameras' talk password"]
	for what, err := range map[string]error{
		"brain":     config.Set().Brain().SetKey(fakeSecrets["the voice assistant's key"]),
		"realtime":  config.Set().Realtime().SetKey(fakeSecrets["realtime voice's key"]),
		"mcp":       config.Set().Realtime().SetMCPToken(fakeSecrets["realtime voice's token"]),
		"music":     config.Set().MusicAssistant().Server(nowhere, &music),
		"reolink":   config.Set().Home().Reolink(config.Reolink{Base: "http://192.168.1.30", User: "admin", Pass: fakeSecrets["the Reolink password"]}),
		"talkback":  config.Set().TalkBack().Save("admin", &talk, nil),
		"camstream": config.Set().CamStream().Login("recorder", fakeSecrets["the camera stream's password"]),
		"dashboard": config.Set().Dashboard().Server("192.168.1.20:9555", fakeSecrets["the dashboard server's key"]),
		"deck":      config.Set().Deck().OBS("192.168.1.20", fakeSecrets["the OBS password"]),
		"immich":    config.Set().Home().Immich(config.Immich{Server: nowhere, Key: fakeSecrets["the Immich key"]}),
		"go2rtc":    config.Set().Home().Go2RTC(config.Go2RTC{Base: "http://viewer:" + fakeSecrets["the go2rtc address's password"] + "@192.168.1.20:1984"}),
	} {
		if err != nil {
			t.Fatalf("saving the %s secret: %v", what, err)
		}
	}
}

// Every section that holds a secret, drawn with one saved: the secret is not in the page, in a field's
// value or anywhere else, and the page says one is saved in the same words each time. A section this
// build does not have draws nothing, and has nothing to show.
func TestNoSectionShowsASavedSecret(t *testing.T) {
	saveFakeSecrets(t)
	c := config.Get()
	for what, got := range map[string]string{
		"the voice assistant's key": c.Brain.Key, "realtime voice's key": c.Realtime.Key, "realtime voice's token": c.Realtime.MCPToken,
		"Music Assistant's token": c.MusicAssistant.Token, "the Reolink password": c.Home.Reolink.Pass,
		"the cameras' talk password": c.TalkBack.Pass, "the camera stream's password": c.CamStream.Pass,
		"the dashboard server's key": c.Dashboard.Key, "the OBS password": c.Deck.OBS.Password, "the Immich key": c.Home.Immich.Key,
	} {
		if got != fakeSecrets[what] {
			t.Fatalf("%s was not saved for the test: %q", what, got)
		}
	}

	sections := map[string]func(w http.ResponseWriter){
		"voice assistant":      func(w http.ResponseWriter) { brainSection(w, "tok") },
		"realtime voice":       func(w http.ResponseWriter) { realtimeSection(w, "tok") },
		"Music Assistant":      func(w http.ResponseWriter) { musicSection(context.Background(), w, "tok") },
		"Reolink cameras":      func(w http.ResponseWriter) { reolinkSection(w, "tok") },
		"talk through cameras": func(w http.ResponseWriter) { talkBackSection(w, "tok") },
		"camera streaming":     func(w http.ResponseWriter) { camStreamSection(w, "tok") },
		"dashboard server":     func(w http.ResponseWriter) { dashboardSection(w, "tok") },
		"deck":                 func(w http.ResponseWriter) { deckSection(w, "tok") },
		"Immich":               func(w http.ResponseWriter) { immichSection(w, "tok") },
		"go2rtc":               func(w http.ResponseWriter) { go2rtcSection(w, "tok") },
	}
	drawn := 0
	for name, draw := range sections {
		w := httptest.NewRecorder()
		draw(w)
		page := w.Body.String()
		for what, secret := range fakeSecrets {
			if strings.Contains(page, secret) {
				t.Errorf("the %s section shows %s", name, what)
			}
		}
		if page == "" {
			continue // not on this build
		}
		drawn++
		if name == "go2rtc" {
			// Its secret is part of an address, in an ordinary field: the address is shown without it.
			if !strings.Contains(page, `value="http://192.168.1.20:1984"`) || !strings.Contains(page, "shown as") {
				t.Errorf("the go2rtc section: %s", first(page))
			}
			continue
		}
		if !strings.Contains(page, secretSaved) {
			t.Errorf("the %s section does not say a secret is saved", name)
		}
		if n := strings.Count(page, secretSaved); name == "realtime voice" && n != 2 {
			t.Errorf("realtime voice says %d of its two secrets are saved", n)
		}
	}
	// The sections every build has, at the least: an empty page everywhere would pass all of the above.
	if drawn < 5 {
		t.Errorf("only %d sections were drawn", drawn)
	}
}

// passwordFields are the password fields in a page.
var passwordFields = regexp.MustCompile(`(?s)<input\b[^>]*type="password"[^>]*>`)

// On every tab, with every secret saved: no secret in the page, and every password field empty and
// closed to a password manager's sign-in.
func TestEveryPasswordFieldOnEveryTab(t *testing.T) {
	saveFakeSecrets(t)
	f := build()
	f.Open()
	c := ask(t, f)
	f.press()
	fields := 0
	for _, tb := range shownTabs() {
		page := get(f, "/setup?tab="+tb.id, c).Body.String()
		for what, secret := range fakeSecrets {
			if strings.Contains(page, secret) {
				t.Errorf("tab %s shows %s", tb.id, what)
			}
		}
		for _, field := range passwordFields.FindAllString(page, -1) {
			fields++
			if !strings.Contains(field, `autocomplete="new-password"`) {
				t.Errorf("tab %s: a password field a password manager may fill: %s", tb.id, field)
			}
			if !strings.Contains(field, `value=""`) && strings.Contains(field, "value=") {
				t.Errorf("tab %s: a password field drawn with something in it: %s", tb.id, field)
			}
		}
	}
	if fields < 4 {
		t.Errorf("only %d password fields were found on the page", fields)
	}
}

// The Reolink password can be taken away on its own: the address, the user and the cameras found stay,
// and nothing else typed on the form is taken with it.
func TestTheReolinkPasswordCanBeRemoved(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := config.Reolink{Base: "http://192.168.1.30", User: "viewer", Pass: "walnut-fake-recorder-pass",
		Cameras: []config.Camera{{Entity: "reolink:0", Name: "Porch"}}}
	if err := config.Set().Home().Reolink(was); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	reolinkSection(w, "tok")
	if !strings.Contains(w.Body.String(), `name="nopass" value="yes"> Remove the saved one`) {
		t.Fatal("the section does not offer to remove the saved password")
	}

	// The box wins over a password typed beside it, and an address typed with it is not kept unproven.
	v := url.Values{"host": {"192.168.1.40"}, "user": {"somebody"}, "pass": {"walnut-fake-other-pass"}, "nopass": {"yes"}}
	if p := saveReolink(form(v)); p != "" {
		t.Fatal(p)
	}
	got := config.Get().Home.Reolink
	if got.Pass != "" {
		t.Errorf("the password is still %q", got.Pass)
	}
	if got.Base != was.Base || got.User != was.User || len(got.Cameras) != 1 || got.Cameras[0].Name != "Porch" {
		t.Errorf("removing the password changed the rest: %+v", got)
	}
	// With none saved there is nothing to offer, and the box does nothing.
	w = httptest.NewRecorder()
	reolinkSection(w, "tok")
	if page := w.Body.String(); strings.Contains(page, "nopass") || !strings.Contains(page, secretNone) {
		t.Error("with no password saved the section still offers to remove one")
	}
	if p := saveReolink(form(url.Values{"nopass": {"yes"}})); p != "" {
		t.Errorf("removing a password that is not there: %s", p)
	}
}

// The talk-back password can be taken away on its own too, and the user stays.
func TestTheTalkBackPasswordCanBeRemoved(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	pass := "walnut-fake-talk-pass"
	if err := config.Set().TalkBack().Save("viewer", &pass, nil); err != nil {
		t.Fatal(err)
	}
	// Left empty, it is kept, as before.
	if p := saveTalkBack(form(url.Values{"user": {"viewer"}})); p != "" || config.Get().TalkBack.Pass != pass {
		t.Fatalf("an empty field changed the password (%s)", p)
	}
	if p := saveTalkBack(form(url.Values{"user": {"viewer"}, "pass": {"walnut-fake-other-pass"}, "nopass": {"yes"}})); p != "" {
		t.Fatal(p)
	}
	if got := config.Get().TalkBack; got.Pass != "" || got.User != "viewer" {
		t.Errorf("after removing the password: user %q, password %q", got.User, got.Pass)
	}
}
