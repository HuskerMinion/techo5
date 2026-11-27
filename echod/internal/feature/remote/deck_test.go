//go:build !dot && !spot

package remote

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/deck"
	"github.com/HuskerMinion/techo5/echod/internal/lib/deckwire"
)

const (
	testPCKey  = "ABCD-EFGH-JKLM-NPQR-STUV-WXYZ-2345-6789"
	testPCAddr = "198.51.100.7:52930"
	testOBSPw  = "obs-password-not-for-apps"
)

var runButton = config.DeckButton{Label: "Backup", Action: config.DeckPCRun, Computer: "Studio", Value: "backup.sh"}

// testDeck is the API with a deck: a paired computer whose agent lists two apps, OBS with a password,
// and a first page holding a scene button and a script button made on the setup page.
func testDeck(t *testing.T) (*Remote, *int) {
	t.Helper()
	f, _, _ := testRemote(t)
	w := config.Set().Deck()
	if err := w.OBS("198.51.100.8", testOBSPw); err != nil {
		t.Fatal(err)
	}
	if err := w.PairComputer(config.DeckComputer{Name: "Studio", Addr: testPCAddr, Key: testPCKey}); err != nil {
		t.Fatal(err)
	}
	if err := w.Page(0, []config.DeckButton{{Label: "Live", Action: config.DeckOBSScene, Scene: "Live"}, runButton}); err != nil {
		t.Fatal(err)
	}
	oldPCs, oldReload := deckComputers, deckReload
	t.Cleanup(func() { deckComputers, deckReload = oldPCs, oldReload })
	deckComputers = func() map[string]deck.ComputerState {
		return map[string]deck.ComputerState{
			"Studio": {Hello: &deckwire.Hello{Name: "Studio", OS: "darwin", Apps: []string{"Safari", "OBS"}, Scripts: []string{"backup.sh"}}},
			"Gone":   {Hello: &deckwire.Hello{Name: "Gone", Apps: []string{"Safari"}}}, // heard, but no longer paired
		}
	}
	reloads := 0
	deckReload = func() { reloads++ }
	return f, &reloads
}

func putPage(f *Remote, n string, buttons ...config.DeckButton) (int, string) {
	b, _ := json.Marshal(buttons)
	if buttons == nil {
		b = []byte("[]")
	}
	w := call(f, "PUT", "/api/v1/deck/pages/"+n, string(b), true)
	return w.Code, w.Body.String()
}

func TestTheDeckIsReadWithoutItsSecrets(t *testing.T) {
	f, _ := testDeck(t)
	w := call(f, "GET", "/api/v1/deck", "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, secret := range []string{testPCKey, testPCAddr, testOBSPw, `"addr":"198.51.100.7`, `"key"`, `"password"`} {
		if strings.Contains(body, secret) {
			t.Errorf("the deck shows %q: %s", secret, body)
		}
	}
	var v deckView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Cols != config.DeckColsDefault || v.Rows != config.DeckRowsDefault || len(v.Pages) != 1 || len(v.Pages[0].Buttons) != 2 {
		t.Errorf("grid and pages: %+v", v)
	}
	if len(v.Computers) != 1 || v.Computers[0].Name != "Studio" || !v.Computers[0].Connected || strings.Join(v.Computers[0].Apps, ",") != "Safari,OBS" {
		t.Errorf("computers: %+v", v.Computers)
	}
	if !v.OBS.PasswordSet || v.OBS.Addr != "198.51.100.8" {
		t.Errorf("obs: %+v", v.OBS)
	}
	if len(v.Actions) != len(config.DeckActions()) || len(v.Colors) != len(config.DeckColors) || v.Limits.PagesMax != config.DeckPagesMax {
		t.Errorf("actions, colors, limits: %+v %+v %+v", v.Actions, v.Colors, v.Limits)
	}
	for _, a := range v.Actions {
		if want := a.ID != config.DeckPCKeys && a.ID != config.DeckPCType && a.ID != config.DeckPCRun; a.API != want {
			t.Errorf("%s: api %t", a.ID, a.API)
		}
	}
}

func TestTheDeckNeedsASignature(t *testing.T) {
	f, reloads := testDeck(t)
	if w := call(f, "GET", "/api/v1/deck", "", false); w.Code != http.StatusUnauthorized {
		t.Errorf("unsigned read: %d", w.Code)
	}
	if w := call(f, "PUT", "/api/v1/deck/pages/0", "[]", false); w.Code != http.StatusUnauthorized {
		t.Errorf("unsigned write: %d", w.Code)
	}
	if w := call(f, "DELETE", "/api/v1/deck/pages/0", "", false); w.Code != http.StatusUnauthorized {
		t.Errorf("unsigned delete: %d", w.Code)
	}
	if len(config.Get().Deck.Pages[0].Buttons) != 2 || *reloads != 0 {
		t.Error("an unsigned request changed the deck")
	}
}

func TestAPageOfOBSAndOpenButtonsIsSaved(t *testing.T) {
	f, reloads := testDeck(t)
	page := []config.DeckButton{
		{Label: "Live", Action: config.DeckOBSScene, Scene: "Live"},
		runButton, // kept, unchanged in its place
		{Label: " Mic ", Icon: "mdi:microphone-off", Color: "red", Action: config.DeckOBSMute, Input: "Mic", Scene: "dropped"},
		{Action: config.DeckOBSSource, Scene: "Live", Source: "Cam"},
		{Action: config.DeckOBSStream},
		{Label: "OBS", Action: config.DeckPCOpen, Computer: "Studio", Value: "OBS"},
	}
	if code, body := putPage(f, "0", page...); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	got := config.Get().Deck.Pages[0].Buttons
	if len(got) != 12 {
		t.Fatalf("the page is the grid's 12 squares: %d", len(got))
	}
	if got[1] != runButton {
		t.Errorf("the script button: %+v", got[1])
	}
	if want := (config.DeckButton{Label: "Mic", Icon: "microphone-off", Color: "red", Action: config.DeckOBSMute, Input: "Mic"}); got[2] != want {
		t.Errorf("the mute button is tidied as the setup page tidies it: %+v", got[2])
	}
	if got[5].Action != config.DeckPCOpen || got[5].Value != "OBS" || got[6] != (config.DeckButton{}) {
		t.Errorf("open and empty: %+v %+v", got[5], got[6])
	}
	if *reloads != 1 {
		t.Errorf("reloads %d", *reloads)
	}

	// The next page is added, and no further.
	if code, body := putPage(f, "1", config.DeckButton{Action: config.DeckOBSRecord}); code != http.StatusOK || len(config.Get().Deck.Pages) != 2 {
		t.Errorf("adding page 2: %d %s", code, body)
	}
	if code, _ := putPage(f, "3", config.DeckButton{Action: config.DeckOBSRecord}); code != http.StatusNotFound {
		t.Errorf("a page past the next: %d", code)
	}
}

func TestKeysTypeAndRunAreTheSetupPagesOnly(t *testing.T) {
	f, _ := testDeck(t)
	live := config.DeckButton{Label: "Live", Action: config.DeckOBSScene, Scene: "Live"}
	for name, page := range map[string][]config.DeckButton{
		"make keys":        {live, runButton, {Action: config.DeckPCKeys, Computer: "Studio", Value: "cmd+q"}},
		"make type":        {live, runButton, {Action: config.DeckPCType, Computer: "Studio", Value: "hello"}},
		"make run":         {live, runButton, {Action: config.DeckPCRun, Computer: "Studio", Value: "backup.sh"}},
		"change run":       {live, {Label: "Backup", Action: config.DeckPCRun, Computer: "Studio", Value: "rm.sh"}},
		"relabel run":      {live, {Label: "Other", Action: config.DeckPCRun, Computer: "Studio", Value: "backup.sh"}},
		"remove run":       {live},
		"replace run":      {live, {Action: config.DeckOBSStream}},
		"move run":         {live, {}, runButton},
		"open unlisted":    {live, runButton, {Action: config.DeckPCOpen, Computer: "Studio", Value: "Terminal"}},
		"open a website":   {live, runButton, {Action: config.DeckPCOpen, Computer: "Studio", Value: "https://example.com"}},
		"open unpaired":    {live, runButton, {Action: config.DeckPCOpen, Computer: "Gone", Value: "Safari"}},
		"open no computer": {live, runButton, {Action: config.DeckPCOpen, Value: "Safari"}},
		"no scene":         {live, runButton, {Action: config.DeckOBSScene}},
		"unknown action":   {live, runButton, {Action: "pc_shell"}},
		"unknown color":    {live, runButton, {Action: config.DeckOBSStream, Color: "pink"}},
		"long label":       {live, runButton, {Action: config.DeckOBSStream, Label: strings.Repeat("x", config.DeckTextMax+1)}},
		"past the grid":    make([]config.DeckButton, 13),
	} {
		code, body := putPage(f, "0", page...)
		if code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, code, body)
		}
		if got := config.Get().Deck.Pages[0].Buttons; len(got) != 2 || got[1] != runButton {
			t.Errorf("%s changed the page: %+v", name, got)
		}
	}
	if w := call(f, "PUT", "/api/v1/deck/pages/0", `[{"action":"obs_stream","colr":"red"}]`, true); w.Code != http.StatusBadRequest {
		t.Errorf("a misspelled field: %d", w.Code)
	}
	if w := call(f, "DELETE", "/api/v1/deck/pages/0", "", true); w.Code != http.StatusBadRequest || len(config.Get().Deck.Pages) != 1 {
		t.Errorf("removing a page with a script button: %d %s", w.Code, w.Body)
	}
}

// A button of the setup page's own is read without its value (the keys, text or script, which can be a
// password typed into a session), and the page read and sent back as it came keeps it whole: it is
// unchanged. A value sent in its place is still a change, refused.
func TestASetupPageButtonIsReadWithoutItsValueAndKept(t *testing.T) {
	f, _ := testDeck(t)
	typed := config.DeckButton{Label: "Sign in", Action: config.DeckPCType, Computer: "Studio", Value: "hunter2"}
	keys := config.DeckButton{Label: "Quit", Action: config.DeckPCKeys, Computer: "Studio", Value: "cmd+q"}
	open := config.DeckButton{Label: "Web", Action: config.DeckPCOpen, Computer: "Studio", Value: "Safari"}
	if err := config.Set().Deck().Page(0, []config.DeckButton{{Label: "Live", Action: config.DeckOBSScene, Scene: "Live"}, runButton, typed, keys, open}); err != nil {
		t.Fatal(err)
	}
	w := call(f, "GET", "/api/v1/deck", "", true)
	for _, secret := range []string{"hunter2", "cmd+q", "backup.sh"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("the deck shows %q: %s", secret, w.Body)
		}
	}
	var v deckView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if got := v.Pages[0].Buttons[4]; got != open {
		t.Errorf("an open button read as %+v, want its app shown", got)
	}

	// Sent back as read, one button relabeled: saved, and the setup page's buttons kept whole.
	page := v.Pages[0].Buttons
	page[0].Label = "On air"
	code, body := putPage(f, "0", page...)
	if code != http.StatusOK {
		t.Fatalf("the page sent back as read: %d %s", code, body)
	}
	got := config.Get().Deck.Pages[0].Buttons
	if got[0].Label != "On air" || got[1] != runButton || got[2] != typed || got[3] != keys || got[4] != open {
		t.Errorf("the page saved as %+v", got)
	}
	if strings.Contains(body, "hunter2") {
		t.Errorf("the answer to the PUT shows the typed text: %s", body)
	}

	// The real value still counts as unchanged; another is a change, and refused.
	if code, body := putPage(f, "0", got...); code != http.StatusOK {
		t.Errorf("the page sent back with the values: %d %s", code, body)
	}
	other := append([]config.DeckButton{}, page...)
	other[2].Value = "other"
	if code, _ := putPage(f, "0", other...); code != http.StatusBadRequest {
		t.Errorf("a typed text changed through the API: %d", code)
	}
	if got := config.Get().Deck.Pages[0].Buttons; got[2] != typed {
		t.Errorf("the typed text is now %+v", got[2])
	}
}

func TestPagesAreAddedAndRemovedWithinTheLimit(t *testing.T) {
	f, _ := testDeck(t)
	for n := 1; n < config.DeckPagesMax; n++ {
		if code, body := putPage(f, strconv.Itoa(n)); code != http.StatusOK {
			t.Fatalf("page %d: %d %s", n, code, body)
		}
	}
	if code, _ := putPage(f, strconv.Itoa(config.DeckPagesMax)); code != http.StatusBadRequest {
		t.Errorf("an eleventh page: %d", code)
	}
	for _, bad := range []string{"-1", "01", "x", ""} {
		if code, _ := putPage(f, bad); code != http.StatusNotFound {
			t.Errorf("page %q: %d", bad, code)
		}
	}
	if w := call(f, "DELETE", "/api/v1/deck/pages/3", "", true); w.Code != http.StatusOK || len(config.Get().Deck.Pages) != config.DeckPagesMax-1 {
		t.Errorf("removing page 4: %d %s", w.Code, w.Body)
	}
	if w := call(f, "DELETE", "/api/v1/deck/pages/9", "", true); w.Code != http.StatusNotFound {
		t.Errorf("removing a page that is not there: %d", w.Code)
	}
}
