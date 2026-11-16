package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A page with an answer of its own for being off gives that; the rest are not found, as before; and a
// page that is on is served as it always was.
func TestAPageThatIsOffAnswersInItsOwnWordsIfItHasThem(t *testing.T) {
	on := false
	f := &Feature{pages: []page{
		{path: "/own", open: func() bool { return on }, h: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "served") },
			closed: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, "closed, in its own words")
			}},
		{path: "/plain", open: func() bool { return on }, h: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "served") }},
	}}
	m := f.mux()
	get := func(path string) (int, string) {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Code, w.Body.String()
	}
	if code, body := get("/own"); code != http.StatusNotFound || body != "closed, in its own words" {
		t.Errorf("off, the page with its own answer gave %d %q", code, body)
	}
	if code, body := get("/plain"); code != http.StatusNotFound || body != "404 page not found\n" {
		t.Errorf("off, the page with no answer of its own gave %d %q", code, body)
	}
	on = true
	for _, path := range []string{"/own", "/plain"} {
		if code, body := get(path); code != http.StatusOK || body != "served" {
			t.Errorf("on, %s gave %d %q", path, code, body)
		}
	}
}

// Closed reaches the page it names and no other.
func TestClosedIsHandedToThePathItNames(t *testing.T) {
	f := Get()
	f.mu.Lock()
	was := f.pages
	f.pages = nil
	f.mu.Unlock()
	t.Cleanup(func() {
		f.mu.Lock()
		f.pages = was
		f.mu.Unlock()
	})
	never := func() bool { return false }
	h := func(http.ResponseWriter, *http.Request) {}
	Handle("/one", "", never, h)
	Handle("/two", "", never, h)
	Closed("/one", h)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pages[0].closed == nil || f.pages[1].closed != nil {
		t.Error("the answer for a page that is off went to the wrong page")
	}
}
