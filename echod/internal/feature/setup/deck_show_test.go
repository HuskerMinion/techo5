//go:build !dot && !spot

package setup

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func deckPost(form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestDeckOBSForm(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if p := saveDeckOBS(deckPost(url.Values{"address": {"pc.local/x"}})); p == "" {
		t.Error("a bad address was saved")
	}
	if p := saveDeckOBS(deckPost(url.Values{"address": {"192.168.1.20"}, "password": {"pw"}})); p != "" {
		t.Fatal(p)
	}
	if p := saveDeckOBS(deckPost(url.Values{"address": {"192.168.1.20:4456"}})); p != "" {
		t.Fatal(p)
	}
	if o := config.Get().Deck.OBS; o.Addr != "192.168.1.20:4456" || o.Password != "pw" {
		t.Errorf("obs %+v: an empty password box should keep the saved one", o)
	}
	_ = saveDeckOBS(deckPost(url.Values{"address": {"192.168.1.20"}, "nopassword": {"yes"}}))
	if config.Get().Deck.OBS.Password != "" {
		t.Error("the no-password box did not clear it")
	}
}

func pageForm(page string, rows ...[6]string) url.Values {
	f := url.Values{"page": {page}}
	for i, r := range rows {
		n := string(rune('0' + i))
		f.Set("label"+n, r[0])
		f.Set("action"+n, r[1])
		f.Set("target"+n, r[2])
		f.Set("source"+n, r[3])
		f.Set("color"+n, r[4])
		f.Set("icon"+n, r[5])
	}
	return f
}

func TestDeckPageForm(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Deck().Grid(2, 2)
	f := pageForm("0",
		[6]string{"BRB", "obs_scene", "BRB", "", "blue", "mdi:coffee"},
		[6]string{"", "obs_mute", "Mic", "ignored", "", ""},
		[6]string{"Cam", "obs_source", "Main", "Webcam", "", ""},
		[6]string{"", "", "", "", "", ""})
	if p := saveDeckPage(deckPost(f)); p != "" {
		t.Fatal(p)
	}
	d := config.Get().Deck
	if b := d.Button(0, 0); b.Scene != "BRB" || b.Icon != "coffee" || b.Color != "blue" {
		t.Errorf("scene button %+v", b)
	}
	if b := d.Button(0, 1); b.Input != "Mic" || b.Scene != "" || b.Source != "" {
		t.Errorf("mute button %+v: the box is the input, and a source is only for show/hide", b)
	}
	if b := d.Button(0, 2); b.Scene != "Main" || b.Source != "Webcam" {
		t.Errorf("source button %+v", b)
	}
	if !d.Set() {
		t.Error("the deck should count as set up")
	}

	// A row missing what it acts on is refused, naming the row, and nothing on the page changes.
	bad := pageForm("0", [6]string{"New", "obs_scene", "Other", "", "", ""}, [6]string{"", "obs_mute", "", "", "", ""})
	if p := saveDeckPage(deckPost(bad)); !strings.Contains(p, "row 1, button 2") {
		t.Errorf("problem %q", p)
	}
	if config.Get().Deck.Button(0, 0).Scene != "BRB" {
		t.Error("a refused page was half saved")
	}

	// An action the page never offered.
	if p := saveDeckPage(deckPost(pageForm("0", [6]string{"x", "run_shell", "", "", "", ""}))); p == "" {
		t.Error("an unknown action was accepted")
	}

	// Add a page from the last one, then remove the first: the second moves up.
	add := pageForm("0", [6]string{"BRB", "obs_scene", "BRB", "", "", ""})
	add.Set("op", "add")
	if p := saveDeckPage(deckPost(add)); p != "" {
		t.Fatal(p)
	}
	if n := len(config.Get().Deck.Pages); n != 2 {
		t.Fatalf("%d pages after adding one", n)
	}
	if p := saveDeckPage(deckPost(pageForm("1", [6]string{"Stream", "obs_stream", "", "", "red", ""}))); p != "" {
		t.Fatal(p)
	}
	rm := url.Values{"page": {"0"}, "op": {"remove"}}
	if p := saveDeckPage(deckPost(rm)); p != "" {
		t.Fatal(p)
	}
	if b := config.Get().Deck.Button(0, 0); b.Action != config.DeckOBSStream {
		t.Errorf("after removing page 1, its first button is %+v", b)
	}
}

func TestDeckSectionEscapes(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	_ = config.Set().Deck().Page(0, []config.DeckButton{{Label: `<script>x</script>`, Action: config.DeckOBSScene, Scene: `"><b>`}})
	w := httptest.NewRecorder()
	deckSection(w, "tok")
	body := w.Body.String()
	if strings.Contains(body, "<script>x") || strings.Contains(body, `"><b>`) {
		t.Error("a button's text reached the page unescaped")
	}
	if !strings.Contains(body, "Page 1") || !strings.Contains(body, `name="label0"`) {
		t.Error("the page's form is missing")
	}
}
