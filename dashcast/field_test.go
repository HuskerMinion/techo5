package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// A text field inside a component's shadow root, as Home Assistant's are, tapped on the screen, says
// so with its text and label; the device's typing goes into it as a keyboard's would, Enter included;
// Done lets go of it and says that too. A checkbox brings up nothing. Moving from one field to another
// keeps the keyboard up, and a password's text is never sent, nor a one-time code's. A field the page
// focuses by itself brings up nothing until it is tapped.
//
// It needs a real browser, as TestOpenATabInTheRunningBrowser does; CHROME names one on a PC.
func TestTypingIntoAFieldOnThePage(t *testing.T) {
	required := os.Getenv("DASHCAST_REQUIRE_BROWSER") != ""
	chrome := os.Getenv("CHROME")
	if chromePath(chrome) == "" {
		if required {
			t.Fatal("no Chrome or headless-shell to run")
		}
		t.Skip("no Chrome or headless-shell to run")
	}
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>dashboard</title>
<style>body{margin:0}</style>
<search-card style="display:block;height:60px"></search-card>
<input id=pass type=password value=secret style="position:absolute;top:100px;left:0;width:200px;height:40px">
<input id=box type=checkbox style="position:absolute;top:160px;left:0;width:40px;height:40px">
<input id=code autocomplete=one-time-code value=123456 style="position:absolute;top:220px;left:0;width:200px;height:40px">
<script>
customElements.define("search-card", class extends HTMLElement {
  constructor() {
    super();
    const root = this.attachShadow({mode: "open"});
    root.innerHTML = '<form><input id=q aria-label="Search music" value="Abba" style="width:200px;height:40px"></form>';
    const q = root.getElementById("q");
    q.addEventListener("input", () => { window.typed = q.value; });
    root.querySelector("form").addEventListener("submit", (e) => { e.preventDefault(); window.searched = q.value; });
  }
});
</script>`))
	}))
	defer ha.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b, err := newBrowser(ctx, config{ha: ha.URL, chrome: chrome})
	if err != nil {
		if required {
			t.Fatalf("the browser did not start: %v", err)
		}
		t.Skipf("the browser did not start: %v", err)
	}
	defer b.close()
	tab, closeTab, err := b.open(ctx, "/lovelace/0", 480, 320, map[string]bool{"lovelace": true}, false)
	if err != nil {
		t.Fatal(err)
	}
	w := newWarmTab(tab, closeTab, "typing")
	defer w.close()
	said := make(chan field, 8)
	w.watchField(func(f field) { said <- f })

	next := func(what string) field {
		t.Helper()
		select {
		case f := <-said:
			return f
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the page said nothing", what)
		}
		return field{}
	}
	quiet := func(what string) {
		t.Helper()
		select {
		case f := <-said:
			t.Errorf("%s: the page said %+v", what, f)
		case <-time.After(500 * time.Millisecond):
		}
	}
	run := func(what string, a chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(tab, a); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	tap := func(x, y int) chromedp.Action {
		return chromedp.ActionFunc(func(ctx context.Context) error { return touch(ctx, touchMsg{T: "tap", X: x, Y: y}) })
	}
	value := func(js string) string {
		t.Helper()
		var s string
		run("reading the page", chromedp.Evaluate(js, &s))
		return s
	}
	// The page has loaded when its card has.
	for value(`String(!!document.querySelector("search-card").shadowRoot)`) != "true" {
		time.Sleep(50 * time.Millisecond)
	}

	run("the tap on the search field", tap(50, 20))
	if f := next("the search field"); !f.Up || f.Kind != "text" || f.Value != "Abba" || f.Label != "Search music" {
		t.Errorf("the search field: %+v", f)
	}
	run("typing", chromedp.ActionFunc(func(ctx context.Context) error {
		if err := touch(ctx, touchMsg{T: "text", S: " Gold"}); err != nil {
			return err
		}
		return touch(ctx, touchMsg{T: "key", K: "Backspace"})
	}))
	if got := value(`String(window.typed)`); got != "Abba Gol" {
		t.Errorf("typed %q, want the field's text with the typing on the end", got)
	}
	run("Enter", chromedp.ActionFunc(func(ctx context.Context) error { return touch(ctx, touchMsg{T: "key", K: "Enter"}) }))
	if got := value(`String(window.searched)`); got != "Abba Gol" {
		t.Errorf("Enter searched %q", got)
	}

	run("the tap on the password", tap(50, 120))
	if f := next("the password"); !f.Up || f.Kind != "password" || f.Value != "" {
		t.Errorf("moving to the password: %+v, want it up with no text", f)
	}
	quiet("moving from one field to another") // no "down" between them

	run("Done", chromedp.ActionFunc(func(ctx context.Context) error { return touch(ctx, touchMsg{T: "key", K: "done"}) }))
	if f := next("Done"); f.Up {
		t.Errorf("Done: %+v, want the field let go of", f)
	}
	run("the tap on the checkbox", tap(20, 180))
	quiet("a checkbox")

	// Well after the last touch, as a page loading by itself is.
	time.Sleep(3100 * time.Millisecond)
	var none any
	run("the page focusing a field itself", chromedp.Evaluate(`document.getElementById("code").focus()`, &none))
	quiet("a field the page focused")
	run("the tap on the focused field", tap(50, 240))
	if f := next("the one-time code"); !f.Up || f.Value != "" {
		t.Errorf("the one-time code: %+v, want it up with no text", f)
	}
	run("Done again", chromedp.ActionFunc(func(ctx context.Context) error { return touch(ctx, touchMsg{T: "key", K: "done"}) }))
	if f := next("Done again"); f.Up {
		t.Errorf("Done again: %+v", f)
	}
	quiet("after Done") // and a moment between taps, as a finger takes

	// A screen that leaves with a field in use and comes back to its parked tab gets the keyboard
	// again for a tap on that field: parking let go of it, so the tap gives it focus anew.
	run("the tap on the search field again", tap(50, 20))
	if f := next("the search field again"); !f.Up {
		t.Errorf("the search field again: %+v", f)
	}
	warm.park(w)
	if got := warm.take("typing"); got != w {
		t.Fatal("the parked tab was not handed back")
	}
	w.watchField(func(f field) { said <- f })
	run("the tap after coming back", tap(50, 20))
	f := next("the search field after parking")
	if !f.Up {
		// Letting go of it, said once the page thawed: harmless, as the device has no keyboard up.
		f = next("the search field after parking")
	}
	if !f.Up || f.Value != "Abba Gol" {
		t.Errorf("the search field after parking: %+v", f)
	}
}

// What the page says is held to what the script sends: any script on the page could call it.
func TestParseFieldKeepsToTheScript(t *testing.T) {
	long := strings.Repeat("é", fieldValueMax+10)
	for _, c := range []struct {
		payload string
		ok      bool
		want    field
	}{
		{`{"up":true,"kind":"search","value":"abc","label":" Find "}`, true, field{Up: true, Kind: "search", Value: "abc", Label: "Find"}},
		{`{"up":true,"kind":"password","value":"secret"}`, true, field{Up: true, Kind: "password"}},
		{`{"up":true,"kind":"color","value":"x"}`, true, field{Up: true, Kind: "text", Value: "x"}},
		{`{"up":false,"value":"left over"}`, true, field{}},
		{`{"up":true,"value":"` + long + `"}`, true, field{Up: true, Kind: "text", Value: strings.Repeat("é", fieldValueMax)}},
		{`not json`, false, field{}},
		{`{"up":true,"value":"` + strings.Repeat("x", 10000) + `"}`, false, field{}},
	} {
		got, ok := parseField(c.payload)
		if ok != c.ok || got != c.want {
			t.Errorf("%.60s: %+v %v, want %+v %v", c.payload, got, ok, c.want, c.ok)
		}
	}
}
