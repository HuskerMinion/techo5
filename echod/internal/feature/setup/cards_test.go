package setup

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// cardTag is how a card starts, open or not.
var cardTag = regexp.MustCompile(`<details class="card" id="sec-([^"]+)"( open)?( data-keep)?>`)

// openCards are the cards a page draws open, and every card on it.
func openCards(page string) (open map[string]bool, all []string) {
	open = map[string]bool{}
	for _, m := range cardTag.FindAllStringSubmatch(page, -1) {
		all = append(all, m[1])
		if m[2] != "" {
			open[m[1]] = true
		}
	}
	return open, all
}

// A page that comes back from a save has the card the save was made in open, with what happened said
// in it, and the others shut.
func TestTheCardASaveCameBackToIsOpen(t *testing.T) {
	f, c := in(t)
	to := post(t, f, c, url.Values{"what": {"house"}, "word": {"bluebird"}})
	page := get(f, to.RequestURI(), c).Body.String()
	open, all := openCards(page)
	if !open["house"] {
		t.Fatal("the card the save was made in is shut")
	}
	if len(all) < 2 {
		t.Fatalf("the tab drew %d cards", len(all))
	}
	for _, name := range all {
		if name != "house" && open[name] && !alwaysOpen[name] {
			t.Errorf("%s is open too", name)
		}
	}
	// With nothing to say, a page sent back to a section opens its card all the same.
	if to, _ := url.Parse(back("history", "", "")); to.Query().Get("open") != "history" || to.Fragment != "sec-history" {
		t.Errorf("sent back with nothing to say to %s", to)
	}
}

// open= opens the card it names, or every card (all); anything else opens nothing and is not written
// into the page.
func TestTheAddressOpensCards(t *testing.T) {
	f, c := in(t)
	open, all := openCards(get(f, "/setup?tab=system&open=timezone", c).Body.String())
	if !open["timezone"] {
		t.Error("open=timezone left the time zone shut")
	}
	for _, name := range all {
		if name != "timezone" && open[name] {
			t.Errorf("open=timezone opened %s too", name)
		}
	}

	open, all = openCards(get(f, "/setup?tab=system&open=all", c).Body.String())
	if len(all) == 0 || len(open) != len(all) {
		t.Errorf("open=all opened %d of %d cards", len(open), len(all))
	}

	for _, v := range []string{"nonsense", "sec-timezone", "ALL", `%22%3E%3Cb%3Ehello`, "timezone%20open"} {
		page := get(f, "/setup?tab=system&open="+v, c).Body.String()
		open, all := openCards(page)
		if len(all) == 0 || len(open) != 0 {
			t.Errorf("open=%s opened %v", v, open)
		}
		if strings.Contains(page, "hello") || strings.Contains(page, "nonsense") {
			t.Errorf("open=%s was written into the page", v)
		}
	}
	if openOf("none") != "none" || openOf("all") != "all" || openOf("bogus") != "" || openOf("house") != "house" {
		t.Error("openOf lets through what it should not, or stops what it should not")
	}
}

// The rail lists the sections of the tab being shown, each a link that opens its card, and only that
// tab's; Status, which has no cards, is drawn as it was.
func TestTheRailListsTheTabsSections(t *testing.T) {
	f, c := in(t)
	page := get(f, "/setup?tab=system", c).Body.String()
	start := strings.Index(page, `<ul class="toc">`)
	end := start + strings.Index(page[max(start, 0):], `</ul>`)
	if start < 0 || end < start || start > strings.Index(page, `</nav>`) {
		t.Fatal("no list of sections in the rail")
	}
	toc := page[start:end]
	_, all := openCards(page)
	for _, name := range all {
		if !strings.Contains(toc, `href="/setup?tab=system&amp;open=`+name+`#sec-`+name+`"`) {
			t.Errorf("the rail does not list %s", name)
		}
	}
	if !strings.Contains(toc, `#sec-timezone">Time zone</a>`) {
		t.Error("the rail's link to the time zone is not called Time zone")
	}
	if strings.Count(toc, "<li>") != len(all) {
		t.Errorf("the rail lists %d sections for %d cards", strings.Count(toc, "<li>"), len(all))
	}
	if strings.Contains(toc, "sec-house") || strings.Contains(toc, "sec-speaker") {
		t.Error("the rail lists another tab's sections")
	}
	if !strings.Contains(page, `href="/setup?tab=system&amp;open=all">Open all</a>`) ||
		!strings.Contains(page, `href="/setup?tab=system&amp;open=none">Close all</a>`) ||
		!strings.Contains(page, `localStorage`) {
		t.Error("no Open all and Close all, or no script to keep cards open")
	}

	// Status's cards are always open: nothing to open or shut all, nothing to remember.
	status := get(f, "/setup?tab=status", c).Body.String()
	for _, s := range []string{`class="cardbar"`, "localStorage"} {
		if strings.Contains(status, s) {
			t.Errorf("Status has %s", s)
		}
	}
	for _, s := range []string{`id="sec-now" open data-keep`, `id="sec-switchedon" open data-keep`, `id="sec-diagnostics" open data-keep`} {
		if !strings.Contains(status, s) {
			t.Errorf("Status has no %s", s)
		}
	}
}

// A list that is what its tab is for is open whatever the address says.
func TestTheAlarmsListStaysOpen(t *testing.T) {
	f, c := in(t)
	for _, q := range []string{"", "&open=none", "&open=alarm_prefs", "&open=bogus"} {
		page := get(f, "/setup?tab=alarms"+q, c).Body.String()
		if !strings.Contains(page, `<details class="card" id="sec-alarm_edit" open data-keep>`) {
			t.Errorf("tab=alarms%s: Coming up is shut", q)
		}
		if open, _ := openCards(page); open["alarm_prefs"] != (q == "&open=alarm_prefs") {
			t.Errorf("tab=alarms%s: For every alarm is open: %v", q, open["alarm_prefs"])
		}
	}
}

// Every card a tab opens is closed, and nothing else is closed in its place: a sectionClose missed or
// written twice would leave the rest of the tab inside a card, or close a card early.
func TestEveryCardIsClosed(t *testing.T) {
	saveFakeSecrets(t)
	f := build()
	f.Open()
	c := ask(t, f)
	f.press()
	for _, tb := range shownTabs() {
		page := get(f, "/setup?tab="+tb.id+"&open=all", c).Body.String()
		for _, tag := range []string{"details", "fieldset", "form", "div"} {
			if opened, closed := strings.Count(page, "<"+tag+">")+strings.Count(page, "<"+tag+" "), strings.Count(page, "</"+tag+">"); opened != closed {
				t.Errorf("%s: %d <%s> and %d </%s>", tb.id, opened, tag, closed, tag)
			}
		}
	}
}
