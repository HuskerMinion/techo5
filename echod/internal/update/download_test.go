package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The waits between tries are what a device needs, not what a test can sit through.
func TestMain(m *testing.M) {
	firstBackoff, mostBackoff = time.Millisecond, 5*time.Millisecond
	os.Exit(m.Run())
}

// set changes one of the download's tunables for the length of a test.
func set[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// payload is a file big enough to be cut in the middle, and different at every byte position so a
// resume from the wrong place shows in the hash.
func payload() []byte {
	b := make([]byte, 256<<10)
	for i := range b {
		b[i] = byte(i*7 + i/251)
	}
	return b
}

func offered(url string, body []byte) Binary {
	sum := sha256.Sum256(body)
	return Binary{URL: url, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
}

// asset is a server that answers ranges the way a CDN does, with a test's say on each request first:
// before returns true when it has answered the request itself.
type asset struct {
	body   []byte
	before func(w http.ResponseWriter, r *http.Request, n int) bool

	mu     sync.Mutex
	ranges []string
}

func (a *asset) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	n := len(a.ranges)
	a.ranges = append(a.ranges, r.Header.Get("Range"))
	a.mu.Unlock()
	if a.before != nil && a.before(w, r, n) {
		return
	}
	http.ServeContent(w, r, "echod", time.Time{}, bytes.NewReader(a.body))
}

func (a *asset) asked() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.ranges...)
}

func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// cut sends the first upTo bytes of body as though it were sending all of it, and then drops the
// connection.
func cut(w http.ResponseWriter, body []byte, upTo int) {
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body[:upTo])
	w.(http.Flusher).Flush()
	panic(http.ErrAbortHandler)
}

// arrived checks the file came out whole at to, with nothing left beside it.
func arrived(t *testing.T, to string, want []byte, b Binary) {
	t.Helper()
	got, err := os.ReadFile(to)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("staged %d bytes that are not what was offered", len(got))
	}
	if _, err := os.Stat(partPath(to, b)); err == nil {
		t.Error("the partial download was left behind")
	}
}

// A connection that drops halfway costs the half not yet fetched, not the whole thing.
func TestADroppedDownloadCarriesOnFromWhereItGot(t *testing.T) {
	body := payload()
	half := len(body) / 2
	a := &asset{body: body, before: func(w http.ResponseWriter, _ *http.Request, n int) bool {
		if n == 0 {
			cut(w, body, half)
		}
		return false
	}}
	b := offered(serve(t, a)+"/echod", body)

	to := filepath.Join(t.TempDir(), "echod")
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
	if want := []string{"", "bytes=" + strconv.Itoa(half) + "-"}; !slices.Equal(a.asked(), want) {
		t.Errorf("asked for %q, want %q", a.asked(), want)
	}
}

// A connection that stays open with nothing coming down it is dropped after stallTimeout, said to
// whoever watches the link, and picked up from where it stopped.
func TestAStalledDownloadIsDroppedAndResumed(t *testing.T) {
	set(t, &stallTimeout, 200*time.Millisecond)
	var mu sync.Mutex
	var slow []int64
	OnSlowDownload(func(rate int64) { mu.Lock(); slow = append(slow, rate); mu.Unlock() })
	t.Cleanup(func() { OnSlowDownload(nil) })

	body := payload()
	half := len(body) / 2
	a := &asset{body: body, before: func(w http.ResponseWriter, r *http.Request, n int) bool {
		if n > 0 {
			return false
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body[:half])
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		return true
	}}
	b := offered(serve(t, a)+"/echod", body)

	to := filepath.Join(t.TempDir(), "echod")
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
	if want := []string{"", "bytes=" + strconv.Itoa(half) + "-"}; !slices.Equal(a.asked(), want) {
		t.Errorf("asked for %q, want %q", a.asked(), want)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(slow) != 1 || slow[0] != 0 {
		t.Errorf("the stall was reported as %v, want one report of 0", slow)
	}
}

// A release asset is a redirect to a CDN, so what was asked for has to survive the redirect: the
// rest of the file, not all of it again. What an earlier install left is where it starts.
func TestAResumeAsksTheRedirectedServerForTheRest(t *testing.T) {
	body := payload()
	cdn := &asset{body: body}
	cdnURL := serve(t, cdn)
	var seen string
	origin := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Range")
		http.Redirect(w, r, cdnURL+"/release-asset", http.StatusFound)
	}))
	b := offered(origin+"/echod", body)

	to := filepath.Join(t.TempDir(), "echod")
	kept := 100 << 10
	if err := os.WriteFile(partPath(to, b), body[:kept], 0o644); err != nil {
		t.Fatal(err)
	}
	if got := partial(to, b); got != int64(kept) {
		t.Errorf("partial says %d bytes are kept, want %d", got, kept)
	}

	var first float32 = -1
	err := download(context.Background(), b, to, func(at float32) {
		if first < 0 {
			first = at
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
	want := "bytes=" + strconv.Itoa(kept) + "-"
	if seen != want {
		t.Errorf("the origin was asked for %q, want %q", seen, want)
	}
	if got := cdn.asked(); !slices.Equal(got, []string{want}) {
		t.Errorf("the CDN was asked for %q, want %q", got, []string{want})
	}
	if first < 0.35 {
		t.Errorf("progress began at %v with %d of %d bytes already here", first, kept, len(body))
	}
}

// A server that ignores the range sends the whole file, and the kept bytes are thrown away for it
// rather than having it appended to them.
func TestAServerThatIgnoresTheRangeStartsOver(t *testing.T) {
	body := payload()
	b := offered(serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))+"/echod", body)

	to := filepath.Join(t.TempDir(), "echod")
	if err := os.WriteFile(partPath(to, b), body[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
}

// Kept bytes that are wrong show only in the hash at the end. Then the file is fetched once more
// from nothing, rather than failing the install for a fault in what this device kept.
func TestBadKeptBytesAreFetchedAgainFromNothing(t *testing.T) {
	body := payload()
	a := &asset{body: body}
	b := offered(serve(t, a)+"/echod", body)

	to := filepath.Join(t.TempDir(), "echod")
	if err := os.WriteFile(partPath(to, b), bytes.Repeat([]byte{0xee}, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
	if want := []string{"bytes=1000-", ""}; !slices.Equal(a.asked(), want) {
		t.Errorf("asked for %q, want %q", a.asked(), want)
	}
}

// Bytes kept for another release are not taken for the start of this one.
func TestBytesKeptForAnotherReleaseAreDropped(t *testing.T) {
	body := payload()
	a := &asset{body: body}
	b := offered(serve(t, a)+"/echod", body)

	to := filepath.Join(t.TempDir(), "echod")
	other := to + ".0123456789abcdef.part"
	if err := os.WriteFile(other, body[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
	if _, err := os.Stat(other); err == nil {
		t.Error("another release's partial download was kept")
	}
	if want := []string{""}; !slices.Equal(a.asked(), want) {
		t.Errorf("asked for %q, want %q", a.asked(), want)
	}
}

// A link that never moves is given up on after tries, and what was fetched before it stopped is kept
// for the next install, which carries on from it.
func TestWhatWasFetchedIsKeptForTheNextInstall(t *testing.T) {
	body := payload()
	half := len(body) / 2
	down := true
	var mu sync.Mutex
	a := &asset{body: body, before: func(w http.ResponseWriter, _ *http.Request, n int) bool {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case !down:
			return false
		case n == 0:
			cut(w, body, half)
		default:
			http.Error(w, "busy", http.StatusServiceUnavailable)
		}
		return true
	}}
	b := offered(serve(t, a)+"/echod", body)

	to := filepath.Join(t.TempDir(), "echod")
	if err := download(context.Background(), b, to, nil); err == nil {
		t.Fatal("a download from a server that never came back succeeded")
	}
	// The cut, then tries that got nothing.
	if got := len(a.asked()); got != 1+tries {
		t.Errorf("asked %d times, want %d", got, 1+tries)
	}
	if got := partial(to, b); got != int64(half) {
		t.Fatalf("kept %d bytes, want %d", got, half)
	}
	if _, err := os.Stat(to); err == nil {
		t.Fatal("an unfinished download was left where it would be installed from")
	}

	mu.Lock()
	down = false
	mu.Unlock()
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
	asked := a.asked()
	if last := asked[len(asked)-1]; last != "bytes="+strconv.Itoa(half)+"-" {
		t.Errorf("the next install asked for %q", last)
	}
}

// An answer that another try would only repeat is not tried again.
func TestAMissingFileIsNotTriedAgain(t *testing.T) {
	a := &asset{before: func(w http.ResponseWriter, r *http.Request, _ int) bool {
		http.NotFound(w, r)
		return true
	}}
	b := offered(serve(t, a)+"/echod", payload())
	if err := download(context.Background(), b, filepath.Join(t.TempDir(), "echod"), nil); err == nil {
		t.Fatal("a missing file was downloaded")
	}
	if got := len(a.asked()); got != 1 {
		t.Errorf("asked %d times for a file that is not there", got)
	}
}

// A download that crawls is said to whoever watches the link, with its rate; one that does not is not.
func TestASlowDownloadIsReported(t *testing.T) {
	set(t, &slowWindow, 100*time.Millisecond)
	set(t, &slowRate, int64(1<<20))
	var mu sync.Mutex
	var slow []int64
	OnSlowDownload(func(rate int64) { mu.Lock(); slow = append(slow, rate); mu.Unlock() })
	t.Cleanup(func() { OnSlowDownload(nil) })

	body := payload()[:8<<10]
	b := offered(serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		for i := 0; i < len(body); i += 256 {
			_, _ = w.Write(body[i : i+256])
			w.(http.Flusher).Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))+"/echod", body)

	to := filepath.Join(t.TempDir(), "echod")
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)

	mu.Lock()
	got := append([]int64(nil), slow...)
	slow = nil
	mu.Unlock()
	if len(got) == 0 {
		t.Fatal("a download at about 25 kB/s was not reported as slow")
	}
	for _, r := range got {
		if r <= 0 || r >= slowRate {
			t.Errorf("reported a rate of %d bytes a second", r)
		}
	}

	// The same window and a server that sends it all at once: nothing to say.
	fast := offered(serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))+"/echod", body)
	if err := download(context.Background(), fast, filepath.Join(t.TempDir(), "echod"), nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(slow) != 0 {
		t.Errorf("a fast download was reported as slow: %v", slow)
	}
}

func TestContentRangeIsRead(t *testing.T) {
	for _, tc := range []struct {
		h           string
		from, total int64
		ok          bool
	}{
		{"bytes 100-199/200", 100, 200, true},
		{"bytes 0-0/1", 0, 1, true},
		{"bytes 100-199/*", 100, -1, true},
		{"bytes */200", 0, 200, false},
		{"", 0, -1, false},
		{"items 1-2/3", 0, -1, false},
	} {
		from, total, ok := contentRange(tc.h)
		if from != tc.from || total != tc.total || ok != tc.ok {
			t.Errorf("%q: %d, %d, %v; want %d, %d, %v", tc.h, from, total, ok, tc.from, tc.total, tc.ok)
		}
	}
}
