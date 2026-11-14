package setup

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/streaming"
)

// Fields that do nothing in the choice made are left out of the page by a rule in its style, which
// looks at the choice as it stands in the form: no script, and nothing changes in what is sent. Each
// needs both halves, the rule and the class on what it hides, and either one renamed alone would put
// the fields back without a test failing. These are the rules, as the page sends them.
const (
	directRule  = `form:has(select[name=mode] option[value=""]:checked) .directonly{display:none}`
	spotifyRule = `form:has(input[name=spotify]:not(:checked)) .spotifyonly{display:none}`
	saverRule   = `form:has(select[name=slideshow] option:not([value=` + config.SlideshowScreensaver + `]):checked) .saveronly{display:none}`
	worldRule   = `form:has(select[name=clockstyle] option:not(.world):checked) .worldonly{display:none}`
	fixedRule   = `form:has(input[name=mode][value=dhcp]:checked) .fixedonly{display:none}`
	theseRule   = `form:has(input[name=mode][value=none]:checked) .theseonly,form:has(input[name=mode][value=all]:checked) .theseonly{display:none}`
)

// drawn is a section as the page writes it.
func drawn(section func(w *httptest.ResponseRecorder)) string {
	w := httptest.NewRecorder()
	section(w)
	return w.Body.String()
}

// needs fails when the rule is not in the page's style or a part of the section is not as it should be.
func needs(t *testing.T, rule, section string, parts ...string) {
	t.Helper()
	if !strings.Contains(headCSS(), rule) {
		t.Errorf("the page's style does not have the rule %s", rule)
	}
	for _, p := range parts {
		if !strings.Contains(section, p) {
			t.Errorf("the section does not have %s", p)
		}
	}
}

// The speech servers, the chat model and its key are for answering directly; what realtime voice uses
// as well stays whichever is chosen.
func TestDirectFieldsAreLeftOutForHomeAssistant(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	// With a key saved, since the box that removes one is only drawn when there is one (secret.go).
	if err := config.Set().Brain().SetKey("walnut-fake-brain-key"); err != nil {
		t.Fatal(err)
	}
	s := drawn(func(w *httptest.ResponseRecorder) { brainSection(w, "tok") })
	needs(t, directRule, s, `<select id="mode" name="mode">`, `<option value="" selected>`, `<div class="directonly">`)
	hides := s[strings.Index(s, `<div class="directonly">`):]
	hides = hides[:strings.Index(hides, `</div>`)]
	for _, in := range []string{`name="stt"`, `name="tts"`, `name="llm"`, `name="model"`, `name="key"`, `name="nokey"`} {
		if !strings.Contains(hides, in) {
			t.Errorf("%s is not among what is left out", in)
		}
	}
	for _, out := range []string{`name="search"`, `name="reader"`, `name="news"`, `name="prompt"`, `type="submit"`} {
		if strings.Contains(hides, out) {
			t.Errorf("%s is left out, and realtime voice uses it too", out)
		}
	}
}

// Spotify Connect's quality and its evening out of the volume are for Spotify Connect.
func TestSpotifyChoicesAreLeftOutWithoutSpotify(t *testing.T) {
	if !streaming.Here {
		t.Skip("this device does not offer Spotify Connect")
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	s := drawn(func(w *httptest.ResponseRecorder) { streamingSection(w, "tok") })
	needs(t, spotifyRule, s, `name="spotify" value="yes"`, `<div class="spotifyonly">`)
	hides := s[strings.Index(s, `<div class="spotifyonly">`):]
	hides = hides[:strings.Index(hides, `</div>`)]
	if !strings.Contains(hides, `name="spotifybitrate"`) || !strings.Contains(hides, `name="spotifynormalize"`) ||
		strings.Contains(hides, `name="airplay`) || strings.Contains(hides, `name="dlna"`) {
		t.Errorf("what is left out without Spotify Connect is not its two choices: %s", hides)
	}
}

// The screensaver's clock and weather are for screensaver mode, and the World places for the World style.
func TestScreenChoicesAreLeftOutWhereTheyDoNothing(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	SetScreen(&ScreenChoices{Styles: []string{"Classic", "World"}, Current: func() int { return 0 }, Choose: func(int) {}})
	t.Cleanup(func() { SetScreen(nil) })
	s := drawn(func(w *httptest.ResponseRecorder) { screenSection(w, "tok") })

	needs(t, saverRule, s, `<select id="slideshow" name="slideshow">`,
		`<option value="`+config.SlideshowScreensaver+`"`, `<div class="saveronly">`)
	hides := s[strings.Index(s, `<div class="saveronly">`):]
	hides = hides[:strings.Index(hides, `</div>`)]
	if !strings.Contains(hides, `name="saverclock"`) || !strings.Contains(hides, `name="saverweather"`) ||
		strings.Contains(hides, `type="submit"`) {
		t.Errorf("what is left out without screensaver mode is not the screensaver's clock and weather: %s", hides)
	}

	needs(t, worldRule, s, `<select id="clockstyle" name="clockstyle">`, `<option value="1" class="world">World</option>`,
		`<option value="0" selected>Classic</option>`, `<div class="worldonly">`)
	hides = s[strings.Index(s, `<div class="worldonly">`):]
	hides = hides[:strings.Index(hides, `</div>`)]
	if !strings.Contains(hides, `name="world"`) || strings.Contains(hides, `name="swipe"`) {
		t.Errorf("what is left out without the World style is not its places: %s", hides)
	}
}

// The list of cameras is for These.
func TestTheCameraListIsLeftOutForNoneAndAll(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	s := drawn(func(w *httptest.ResponseRecorder) { haCamerasSection(w, "tok") })
	needs(t, theseRule, s, `name="mode" value="none"`, `name="mode" value="all"`, `name="mode" value="these"`,
		`<div class="theseonly"><textarea id="hacams" name="list"`)
	// What the saver compares the list with is not in what is left out: a browser sends it either way.
	hides := s[strings.Index(s, `<div class="theseonly">`):]
	if hides = hides[:strings.Index(hides, `</div>`)]; strings.Contains(hides, `was_list`) {
		t.Error("the list as it was is left out with the list")
	}
}
