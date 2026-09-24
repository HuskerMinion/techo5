package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// A tab opens in the browser started for it. The first device's connection panicked in chromedp
// ("WithBrowserOption can only be used when allocating a new browser") because open handed the tab a
// browser option (techo5#26); nothing ran a real browser, so no test saw it. This one does.
//
// Where there is no browser - a PC without Chrome - it is skipped, unless DASHCAST_REQUIRE_BROWSER
// is set: the image build runs it inside the image with that set, so the browser dashcast ships
// failing to start is a failure there, never a skip.
func TestOpenATabInTheRunningBrowser(t *testing.T) {
	required := os.Getenv("DASHCAST_REQUIRE_BROWSER") != ""
	noBrowser := func(why string, args ...any) {
		t.Helper()
		if required {
			t.Fatalf(why, args...)
		}
		t.Skipf(why, args...)
	}
	if chromePath("") == "" {
		noBrowser("no Chrome or headless-shell to run")
	}
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>dashboard</title><p>a dashboard"))
	}))
	defer ha.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	b, err := newBrowser(ctx, config{ha: ha.URL})
	if err != nil {
		noBrowser("the browser did not start: %v", err)
	}
	defer b.close()

	for i := range 2 { // a second tab too: every device gets one
		tab, closeTab, err := b.open(ctx, "/lovelace/0", 480, 480, map[string]bool{"lovelace": true})
		if err != nil {
			t.Fatalf("tab %d: %v", i+1, err)
		}
		if tab == nil {
			t.Fatalf("tab %d: no tab", i+1)
		}
		closeTab()
	}
}
