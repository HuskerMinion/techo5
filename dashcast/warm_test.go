package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// A tab is shown, and sends frames, while another screen's tab is open and after it has been parked
// and picked up again. A parked tab came back hidden, and a hidden page sends one frame and then
// none: a screen that came back to its dashboard froze on it, taps and all (#112).
//
// Skipped without a browser, and run inside the image by the image build, as
// TestOpenATabInTheRunningBrowser is.
func TestAParkedTabShowsAgain(t *testing.T) {
	required := os.Getenv("DASHCAST_REQUIRE_BROWSER") != ""
	if chromePath("") == "" {
		if required {
			t.Fatal("no Chrome or headless-shell to run")
		}
		t.Skip("no Chrome or headless-shell to run")
	}
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>dashboard</title><p id=t></p>
<script>setInterval(() => { t.textContent = Date.now() }, 100)</script>`))
	}))
	defer ha.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b, err := newBrowser(ctx, config{ha: ha.URL})
	if err != nil {
		if required {
			t.Fatalf("the browser did not start: %v", err)
		}
		t.Skipf("the browser did not start: %v", err)
	}
	defer b.close()

	open := func(key string) *warmTab {
		tab, closeTab, err := b.open(ctx, "/lovelace/0", 480, 240, map[string]bool{"lovelace": true}, false)
		if err != nil {
			t.Fatal(err)
		}
		return newWarmTab(tab, closeTab, key)
	}
	// showing is the tab's visibility and whether frames keep coming: more than the first one in
	// two seconds of a page that changes ten times a second.
	showing := func(name string, w *warmTab) {
		t.Helper()
		var frames atomic.Int32
		w.watch(func(f *page.EventScreencastFrame) {
			frames.Add(1)
			go func() { _ = chromedp.Run(w.ctx, page.ScreencastFrameAck(f.SessionID)) }()
		})
		defer w.watch(nil)
		var shown string
		if err := chromedp.Run(w.ctx, chromedp.Evaluate(`document.visibilityState`, &shown),
			page.StartScreencast().WithFormat(page.ScreencastFormatPng)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		time.Sleep(2 * time.Second)
		_ = chromedp.Run(w.ctx, page.StopScreencast())
		if n := frames.Load(); shown != "visible" || n < 3 {
			t.Errorf("%s: %s, %d frames in 2 s", name, shown, n)
		}
	}

	a := open("a")
	showing("a new tab", a)
	other := open("b")
	defer other.close()
	showing("another screen's new tab", other)
	showing("the first tab, with another open", a)
	warm.park(a)
	again := warm.take("a")
	if again == nil {
		t.Fatal("the parked tab was not picked up")
	}
	defer again.close()
	showing("the tab parked and picked up again", again)
	showing("the other screen's tab, after", other)
}

// Unset, the warm tab settings are what they always were; a usable value is taken, and one that is
// not is passed over for the default rather than stopping dashcast (#668).
func TestWarmSettings(t *testing.T) {
	for _, c := range []struct {
		forS, countS string
		d            time.Duration
		n            int
	}{
		{"", "", 5 * time.Minute, 4},
		{"12h", "6", 12 * time.Hour, 6},
		{" 90m ", " 1 ", 90 * time.Minute, 1},
		{"16", "", 5 * time.Minute, 4}, // a number with no unit is not a time
		{"soon", "many", 5 * time.Minute, 4},
		{"0s", "0", 5 * time.Minute, 4},
		{"-1h", "-2", 5 * time.Minute, 4},
		{"", "16", 5 * time.Minute, 16},
		{"", "17", 5 * time.Minute, 4}, // past warmCountMax
		{"", "4.5", 5 * time.Minute, 4},
	} {
		d, n := warmSettings(c.forS, c.countS)
		if d != c.d || n != c.n {
			t.Errorf("warmSettings(%q, %q) = %s, %d; want %s, %d", c.forS, c.countS, d, n, c.d, c.n)
		}
	}
	if warmFor != 5*time.Minute || warmCount != 4 {
		t.Errorf("the defaults moved: %s, %d", warmFor, warmCount)
	}
}
