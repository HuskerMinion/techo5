package assistant

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Newer SearXNG sends its quick answers as objects, and all of them were dropped; its infoboxes were
// never read at all.
func TestSearchKeepsQuickAnswersAndInfoboxes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"query":"x","results":[{"title":"A page","url":"https://example.com/a","content":"a  snippet"}],
			"answers":[{"answer":"The team plays at Memorial Stadium.","url":"https://example.com/w"},"An old-style answer"],
			"infoboxes":[{"infobox":"Example team","content":"A football team.","attributes":[{"label":"Inception","value":"1890"},{"label":"Coach","value":null}]}]}`))
	}))
	defer srv.Close()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Brain().Set(config.Brain{Mode: config.BrainDirect, LLM: "x:1", STT: "x:1", TTS: "x:1", Search: srv.URL}); err != nil {
		t.Fatal(err)
	}
	got, err := search("example team stadium")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"answer: The team plays at Memorial Stadium.", "answer: An old-style answer",
		"about Example team: A football team. (Inception 1890)", "1. A page <https://example.com/a> a snippet"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Coach") {
		t.Errorf("kept an attribute with no value:\n%s", got)
	}
	if q := getLastQuery(); q != "example team stadium" {
		t.Errorf("last query %q", q)
	}
	takeFound()
}

// A long page's opening was all that went back, and the game asked about was further down.
func TestPickKeepsTheLinesAskedFor(t *testing.T) {
	var b strings.Builder
	b.WriteString("Football Schedule 2026\n")
	for i := 0; i < 400; i++ {
		b.WriteString("Game notes and more game notes about nothing in particular.\n")
	}
	b.WriteString("Sat, Oct 24\nvs Maryland\n2:30 PM CT\nMemorial Stadium\n")
	for i := 0; i < 400; i++ {
		b.WriteString("Ticket information and parking for every game this year.\n")
	}
	got := pick(b.String(), "Nebraska vs Maryland kickoff time", 2000)
	if len(got) > 2000 {
		t.Errorf("%d bytes, over the 2000 asked", len(got))
	}
	for _, want := range []string{"Football Schedule 2026", "Sat, Oct 24\nvs Maryland\n2:30 PM CT"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "...") {
		t.Errorf("no mark where lines were left out:\n%.300s", got)
	}
	// Nothing to go by: the page's start, as before.
	if got := pick(b.String(), "", 500); !strings.HasPrefix(got, "Football Schedule 2026") || len(got) > 504 {
		t.Errorf("without a want: %.100q (%d bytes)", got, len(got))
	}
	if got := pick("short page", "anything", 500); got != "short page" {
		t.Errorf("a page that fits was changed: %q", got)
	}
}

// One long line (a page with no blocks) is cut in pieces, so a match is not the whole line.
func TestPickCutsALongLine(t *testing.T) {
	text := strings.Repeat("filler words here ", 400) + "the store opens at 9 AM " + strings.Repeat("more filler text ", 400)
	got := pick(text, "store opens", 1000)
	if !strings.Contains(strings.Join(strings.Fields(got), " "), "the store opens at 9 AM") || len(got) > 1000 {
		t.Errorf("got %d bytes: %.200q", len(got), got)
	}
}

func TestPageTextDropsNoticesNotTheContent(t *testing.T) {
	page := `<html><body class="has-nav home">
	<div id="cookie-banner">We use cookies. Accept all</div>
	<div class="site-nav"><a>Sports</a><a>Weather</a></div>
	<div role="navigation"><a>Home</a></div>
	<div hidden>Hidden text</div>
	<div class="ad-slot ad">Buy now</div>
	<div class="navy-schedule"><h2>Navy game</h2><p>Kickoff at noon.</p></div>
	<div class="menu"><p>Pancakes 9 dollars</p></div>
	</body></html>`
	got := pageText(page)
	for _, want := range []string{"Navy game", "Kickoff at noon.", "Pancakes 9 dollars"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, not := range []string{"cookies", "Sports", "Home", "Hidden text", "Buy now"} {
		if strings.Contains(got, not) {
			t.Errorf("kept %q in:\n%s", not, got)
		}
	}
}

// A wrapper named for what sits beside the content holds the content: it is not dropped with its name.
func TestPageTextKeepsAWrapperNamedForItsSidebar(t *testing.T) {
	page := `<html><body><div id="page" class="ad-free">
	<div class="with-sidebar layout">
	 <div class="content"><h2>Store hours</h2><p>Open Monday to Friday, 9 AM to 6 PM.</p>
	  <p>Closed on Sundays and holidays. Curbside pickup until 5 PM.</p>
	  <section><p>The pharmacy opens at 10 AM on Saturdays, and the bakery at 7 AM every day.</p></section></div>
	 <div class="sidebar"><a>Related</a></div>
	</div>
	<div class="ad"><p>Buy now</p></div>
	</div></body></html>`
	got := pageText(page)
	for _, want := range []string{"Store hours", "9 AM to 6 PM", "Closed on Sundays", "pharmacy opens at 10 AM"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, not := range []string{"Related", "Buy now"} {
		if strings.Contains(got, not) {
			t.Errorf("kept %q in:\n%s", not, got)
		}
	}
	// A table is the content, whatever its wrapper is called.
	if got := pageText(`<body><div class="has-sidebar"><table><tr><td>Game 1</td><td>7 PM</td></tr></table></div><p>` +
		strings.Repeat("Other words. ", 40) + `</p></body>`); !strings.Contains(got, "Game 1") {
		t.Errorf("dropped the table:\n%s", got)
	}
}

// A page built by scripts has no text, but it says what it is about to search engines.
func TestReadableKeepsTheStructuredData(t *testing.T) {
	page := `<html><head><meta name="description" content="Store hours and directions.">
	<script type="application/ld+json">{"@context":"https://schema.org","@type":"Store","name":"Example Store",
	 "url":"https://example.com","image":"https://example.com/a.png",
	 "openingHoursSpecification":[{"dayOfWeek":"Saturday","opens":"09:00","closes":"18:00"}]}</script>
	<script>render()</script></head><body><div id="app"></div></body></html>`
	text, data := readable(page)
	if text != "" {
		t.Errorf("text %q", text)
	}
	for _, want := range []string{"Store hours and directions.", "type: Store", "name: Example Store",
		"openingHoursSpecification dayOfWeek: Saturday", "openingHoursSpecification opens: 09:00"} {
		if !strings.Contains(data, want) {
			t.Errorf("missing %q in:\n%s", want, data)
		}
	}
	if strings.Contains(data, "a.png") || strings.Contains(data, "schema.org") {
		t.Errorf("kept an address:\n%s", data)
	}
}

func TestKeywordsDropTheLittleWords(t *testing.T) {
	got := strings.Join(keywords("When does the Costco on 5th open, and is it 24-hour?"), " ")
	if got != "costco 5th open 24 hour" {
		t.Errorf("got %q", got)
	}
}
