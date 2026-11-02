package setup

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/android/logd"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"

	"log/slog"
)

// letInBrowser is a browser that has asked and been let in by the press.
func letInBrowser(t *testing.T) (*Feature, *http.Cookie) {
	t.Helper()
	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	return f, c
}

func follow(t *testing.T, srv *httptest.Server, c *http.Cookie) (*http.Response, *bufio.Scanner) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/setup/log/stream", nil)
	req.AddCookie(c)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp, bufio.NewScanner(resp.Body)
}

// nextData is the next data line of an event stream, or "" when it did not come in time.
func nextData(t *testing.T, sc *bufio.Scanner, within time.Duration) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		for sc.Scan() {
			if s, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				got <- s
				return
			}
		}
		got <- ""
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(within):
		return ""
	}
}

func TestLogViewNeedsThePress(t *testing.T) {
	f := build()
	f.Open()
	waiting := ask(t, f)
	for _, path := range []string{"/setup/log", "/setup/log/stream"} {
		if code := get(f, path, nil).Code; code != http.StatusForbidden {
			t.Errorf("%s with no session got %d, want refused", path, code)
		}
		if code := get(f, path, waiting).Code; code != http.StatusForbidden {
			t.Errorf("%s while only waiting got %d, want refused", path, code)
		}
	}
	f.Close()
	if code := get(f, "/setup/log", nil).Code; code != http.StatusForbidden {
		t.Errorf("the log page while the page is closed got %d, want refused", code)
	}
}

func TestLogPageIsStaticAndScriptSetsOnlyText(t *testing.T) {
	f, c := letInBrowser(t)
	w := get(f, "/setup/log", c)
	if w.Code != http.StatusOK {
		t.Fatalf("log page got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "innerHTML") || strings.Contains(body, "insertAdjacentHTML") {
		t.Error("the log view must not put log text in as markup")
	}
	for _, want := range []string{"textContent", "EventSource", "Download", "Pause", "Errors only", "Warnings and up"} {
		if !strings.Contains(body, want) {
			t.Errorf("log page lacks %q", want)
		}
	}
}

func TestLogStreamRedactsAndEscapes(t *testing.T) {
	old := newLogRedactor
	newLogRedactor = func() func(string) string {
		return func(s string) string { return strings.ReplaceAll(s, "hunter2", "<secret>") }
	}
	defer func() { newLogRedactor = old }()

	f, c := letInBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	defer srv.Close()

	slog.New(logd.NewHandler("t", io.Discard)).Info("before <b>x</b> hunter2")
	resp, sc := follow(t, srv, c)
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	var seen string
	for i := 0; i < logBacklog+1; i++ {
		d := nextData(t, sc, 3*time.Second)
		if d == "" {
			break
		}
		seen = d
		if strings.Contains(d, "before") {
			break
		}
	}
	if strings.Contains(seen, "<b>") || strings.Contains(seen, "<secret>") {
		t.Errorf("markup left raw in the stream: %q", seen)
	}
	if got := decode(t, seen).T; !strings.Contains(got, "<b>x</b> <secret>") || strings.Contains(got, "hunter2") {
		t.Errorf("backlog line not redacted: %q", got)
	}

	slog.New(logd.NewHandler("t", io.Discard)).Error("live hunter2")
	d := nextData(t, sc, 3*time.Second)
	if e := decode(t, d); e.L != "E" || !strings.Contains(e.T, "live <secret>") {
		t.Errorf("live line wrong: %q", d)
	}
}

func TestLogStreamLimitsViewersAndFrees(t *testing.T) {
	f, c := letInBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	defer srv.Close()

	var open []*http.Response
	for i := 0; i < maxLogViewers; i++ {
		resp, _ := follow(t, srv, c)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("viewer %d got %d", i, resp.StatusCode)
		}
		open = append(open, resp)
	}
	resp, _ := follow(t, srv, c)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("one viewer too many got %d, want 429", resp.StatusCode)
	}
	resp.Body.Close()
	open[0].Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for logViewers.Load() >= maxLogViewers && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	again, _ := follow(t, srv, c)
	defer again.Body.Close()
	if again.StatusCode != http.StatusOK {
		t.Errorf("after one left, a viewer got %d", again.StatusCode)
	}
	open[1].Body.Close()
}

// A browser that reads nothing must not slow the logger.
func TestLogStreamSlowReaderDoesNotStallLogging(t *testing.T) {
	f, c := letInBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	defer srv.Close()
	resp, _ := follow(t, srv, c) // never read from
	defer resp.Body.Close()

	log := slog.New(logd.NewHandler("t", io.Discard))
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20000; i++ {
			log.Info("flood", "i", i, "pad", strings.Repeat("p", 200))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("logging stalled behind a browser that does not read")
	}
}

func TestLogStreamResumesAfterLastEventID(t *testing.T) {
	f, c := letInBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	defer srv.Close()
	log := slog.New(logd.NewHandler("t", io.Discard))
	log.Info("one")
	log.Info("two")
	newest := logd.Recently(1)[0].Seq

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/setup/log/stream", nil)
	req.AddCookie(c)
	req.Header.Set("Last-Event-ID", itoa(newest-1))
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	d := nextData(t, bufio.NewScanner(resp.Body), 3*time.Second)
	if !strings.Contains(d, "two") || strings.Contains(d, "one") {
		t.Errorf("resume started with %q, want only the line after the id", d)
	}
}

func decode(t *testing.T, s string) logEntry {
	t.Helper()
	var e logEntry
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		t.Fatalf("not an entry: %q", s)
	}
	return e
}

func itoa(n uint64) string {
	b := []byte{}
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
