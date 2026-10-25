package assistant

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// fakeReader is a page reader that renders every page as the store's hours, counting what it is asked.
func fakeReader(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/read" || r.URL.Query().Get("url") == "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		asked.Add(1)
		w.Write([]byte("<html><body><main><h1>Example Store</h1><p>Open today 10:00 AM to 8:30 PM.</p>" +
			strings.Repeat("<p>Gas station, pharmacy and tire center hours are posted at the door.</p>", 8) + "</main></body></html>"))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func useReader(t *testing.T, reader string) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Brain().Set(config.Brain{Mode: config.BrainDirect, LLM: "x:1", STT: "x:1", TTS: "x:1",
		Search: "http://search.invalid", Reader: reader}); err != nil {
		t.Fatal(err)
	}
}

func openPageGuard(t *testing.T) {
	was := pageAddrOK
	pageAddrOK = func(netip.AddrPort) bool { return true }
	t.Cleanup(func() { pageAddrOK = was })
}

// A page that is empty until its scripts run is read by the page reader.
func TestReaderReadsAScriptPage(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>Store</title></head><body><div id="app">Loading...</div><script>render()</script></body></html>`))
	}))
	t.Cleanup(page.Close)
	reader, asked := fakeReader(t)
	useReader(t, reader.URL)
	openPageGuard(t)
	got, err := readPage(page.URL, "store hours")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Open today 10:00 AM to 8:30 PM.") || asked.Load() != 1 {
		t.Errorf("asked %d times, got:\n%s", asked.Load(), got)
	}
}

// A site that turns a plain request away is read by the page reader too.
func TestReaderReadsATurnedAwayPage(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(page.Close)
	reader, asked := fakeReader(t)
	useReader(t, reader.URL)
	openPageGuard(t)
	if got, err := readPage(page.URL, "hours"); err != nil || !strings.Contains(got, "8:30 PM") || asked.Load() != 1 {
		t.Errorf("asked %d times: %v %q", asked.Load(), err, got)
	}
}

// A page with its text is read as it comes, without the reader.
func TestReaderNotAskedForAPageWithText(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body><main><p>" + strings.Repeat("The game starts at seven. ", 40) + "</p></main></body></html>"))
	}))
	t.Cleanup(page.Close)
	reader, asked := fakeReader(t)
	useReader(t, reader.URL)
	openPageGuard(t)
	if got, err := readPage(page.URL, "game"); err != nil || !strings.Contains(got, "seven") || asked.Load() != 0 {
		t.Errorf("asked %d times: %v %.100q", asked.Load(), err, got)
	}
}

// A page this device will not connect to (this house's network) is not handed to the reader either.
func TestReaderNotAskedForAHousePage(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>the router</body></html>"))
	}))
	t.Cleanup(page.Close)
	reader, asked := fakeReader(t)
	useReader(t, reader.URL)
	// pageAddrOK as it is: the test server is on this machine, which is not the internet.
	if got, err := readPage(page.URL, ""); err == nil || asked.Load() != 0 {
		t.Errorf("asked %d times: %v %q", asked.Load(), err, got)
	}
}

// Without a reader set, a script page says so, as before.
func TestNoReaderScriptPage(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><div id="app"></div></body></html>`))
	}))
	t.Cleanup(page.Close)
	useReader(t, "")
	openPageGuard(t)
	if got, err := readPage(page.URL, ""); err != nil || !strings.Contains(got, "no text to read") {
		t.Errorf("%v %q", err, got)
	}
}
