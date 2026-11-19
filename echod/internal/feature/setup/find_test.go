package setup

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// hitTag is a found card's line naming its tab, and the card under it.
var hitTag = regexp.MustCompile(`<div class="hit"><p class="where">On <a href="/setup\?tab=([a-z]+)&amp;open=([a-z_-]+)#sec-([a-z_-]+)">[^<]*</a></p><details class="card" id="sec-([a-z_-]+)" open`)

// found is the cards a find's page has, in its order, and the tab each is on.
func found(t *testing.T, f *Feature, c *http.Cookie, q string) (names []string, tabOfHit map[string]string, page string) {
	t.Helper()
	page = get(f, "/setup?find="+url.QueryEscape(q), c).Body.String()
	tabOfHit = map[string]string{}
	for _, m := range hitTag.FindAllStringSubmatch(page, -1) {
		if m[2] != m[4] || m[3] != m[4] {
			t.Errorf("a hit links to %s but draws %s", m[2], m[4])
		}
		names = append(names, m[4])
		tabOfHit[m[4]] = m[1]
	}
	return names, tabOfHit, page
}

func has(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// A word in a card's title finds it, first.
func TestFindByTitle(t *testing.T) {
	f, c := in(t)
	names, tabs, page := found(t, f, c, "Zone")
	if len(names) == 0 || names[0] != "timezone" {
		t.Fatalf("a find for zone found %v", names)
	}
	if tabs["timezone"] != "system" {
		t.Errorf("the time zone is said to be on %s", tabs["timezone"])
	}
	if !strings.Contains(page, `<h2>Find: “Zone”</h2>`) || !strings.Contains(page, `value="Zone"`) {
		t.Error("the find is not said back")
	}
	if strings.Contains(page, `class="on"`) || strings.Contains(page, `class="cardbar"`) {
		t.Error("a find's page marks a tab or has the card links")
	}
}

// A word only in a card's notes finds it.
func TestFindByWordsOnTheCard(t *testing.T) {
	f, c := in(t)
	d := findKeywords["diagnostics"]
	for _, k := range d {
		if k == "serial" {
			t.Fatal("serial is a keyword: this test wants a word only on the card")
		}
	}
	if names, _, _ := found(t, f, c, "serial"); !has(names, "diagnostics") {
		t.Errorf("a find for serial found %v", names)
	}
}

// A keyword finds the card it is for, where the card does not say it.
func TestFindByKeyword(t *testing.T) {
	f, c := in(t)
	if names, _, _ := found(t, f, c, "backup"); len(names) == 0 || names[0] != "settingsfile" {
		t.Errorf("a find for backup found %v", names)
	}
	if names, _, _ := found(t, f, c, "vol"); !has(names, "speaker") {
		t.Errorf("a find for vol found %v", names)
	}
}

// Every word has to match.
func TestFindWantsEveryWord(t *testing.T) {
	f, c := in(t)
	names, _, _ := found(t, f, c, "time zone")
	if !has(names, "timezone") || has(names, "timeserver") {
		t.Errorf("a find for time zone found %v", names)
	}
	if names, _, _ := found(t, f, c, "radio stations"); !has(names, "stations") {
		t.Errorf("a find for radio stations found %v", names)
	}
	if names, _, _ := found(t, f, c, "radio qwertyuiop"); len(names) != 0 {
		t.Errorf("a find with a word nothing has found %v", names)
	}
}

func TestFindNothing(t *testing.T) {
	f, c := in(t)
	names, _, page := found(t, f, c, "qwertyuiop")
	if len(names) != 0 || !strings.Contains(page, "No setting matches") || !strings.Contains(page, `href="/setup?tab=system"`) {
		t.Errorf("a find for nothing found %v", names)
	}
	// A find with no words is the tab it was made on.
	page = get(f, "/setup?tab=system&find=+++", c).Body.String()
	if !strings.Contains(page, `href="/setup?tab=system" class="on"`) || strings.Contains(page, "Find: ") {
		t.Error("an empty find did not stay on its tab")
	}
	if !strings.Contains(page, `<input type="hidden" name="tab" value="system"><label class="vh" for="find">Find a setting</label>`) {
		t.Error("the tab has no find box, or the box does not carry the tab")
	}
}

// What was asked is never written into the page as it was typed.
func TestFindIsEscaped(t *testing.T) {
	f, c := in(t)
	q := `<script>alert("x")</script> "><b>`
	_, _, page := found(t, f, c, q)
	for _, raw := range []string{`<script>alert`, `"><b>`, `alert("x")`} {
		if strings.Contains(page, raw) {
			t.Errorf("the page has %q as it was typed", raw)
		}
	}
	if !strings.Contains(page, "&lt;script&gt;alert(&#34;x&#34;)") {
		t.Error("the find is not said back, escaped")
	}
	long := strings.Repeat("x", 500)
	if _, _, page := found(t, f, c, long); strings.Contains(page, strings.Repeat("x", maxFind+1)) {
		t.Error("a long find was not cut")
	}
	if got := findWords("a b c d e f g h i j"); len(got) > maxFindWords {
		t.Errorf("%d words were kept", len(got))
	}
}

// Only what this device has is found: every hit is on a tab it shows.
func TestFindOnlyWhatIsHere(t *testing.T) {
	f, c := in(t)
	shown := map[string]bool{}
	for _, tb := range shownTabs() {
		shown[tb.id] = true
	}
	for _, q := range []string{"on", "the", "setting", "camera"} {
		_, tabs, _ := found(t, f, c, q)
		for name, tb := range tabs {
			if !shown[tb] {
				t.Errorf("%s was found on %s, which this device does not show", name, tb)
			}
		}
	}
}

// A found card's forms are the tab's: the page's token in each, and a save from one comes back to the
// card on its own tab.
func TestASaveFromAFindComesBackToItsTab(t *testing.T) {
	f, c := in(t)
	names, _, page := found(t, f, c, "announcements")
	if !has(names, "house") {
		t.Fatalf("a find for announcements found %v", names)
	}
	card := page[strings.Index(page, `id="sec-house"`):]
	card = card[:strings.Index(card, "</details>")]
	if !strings.Contains(card, `name="token" value="`+c.Value+`"`) || !strings.Contains(card, `name="what" value="house"`) {
		t.Fatal("the found card's form has no token or no what")
	}
	to := post(t, f, c, url.Values{"what": {"house"}, "word": {"bluebird"}})
	if to.Query().Get("tab") != formTab("house") || to.Query().Get("at") != "house" || to.Fragment != "sec-house" {
		t.Errorf("sent back to %s", to)
	}
}

// A find does nothing a tab drawn to be seen would not: it scans for no networks, waits on no server,
// saves nothing, says no save's answer and hands back no refused form.
func TestAFindHasNoSideEffects(t *testing.T) {
	f, c := in(t)
	scans := 0
	was := wifiScan
	wifiScan = func(context.Context) ([]wifi.Network, error) { scans++; return nil, nil }
	t.Cleanup(func() { wifiScan = was })
	rec := httptest.NewRecorder()
	wifiJoinForm(context.Background(), &sectionWriter{ResponseWriter: rec, finding: true}, "t", true)
	if scans != 0 {
		t.Error("a find scanned for networks")
	}
	if waitFor(context.Background(), &sectionWriter{ResponseWriter: rec, finding: true}).Err() == nil {
		t.Error("a find waits on servers")
	}

	// A refused form, kept for its tab.
	to := post(t, f, c, url.Values{"what": {"alarm_add"}, "kind": {"timer"}, "in": {"banana"}})
	if to.Query().Get("problem") == "" {
		t.Fatal("a timer of no length was taken")
	}
	before := fmt.Sprintf("%#v", config.Get())
	page := get(f, "/setup?find=alarm&saved=1&at=house&problem=x&scan=1", c).Body.String()
	if after := fmt.Sprintf("%#v", config.Get()); after != before {
		t.Error("a find changed the saved settings")
	}
	if strings.Contains(page, `class="banner`) {
		t.Error("a find said what a save did")
	}
	if strings.Contains(page, `id="refill"`) {
		t.Error("a find handed back the refused form")
	}
	if refillOf(t, get(f, to.RequestURI(), c).Body.String()) == nil {
		t.Error("a find took the refused form from its tab")
	}
}
