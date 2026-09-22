package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// release stands in for a published build: a manifest describing one binary per architecture, and the
// binaries themselves. Each architecture is served its own bytes, so a test can tell which one was
// fetched from what arrived.
func release(t *testing.T, bodies map[string][]byte) Manifest {
	t.Helper()

	mux := http.NewServeMux()
	m := Manifest{Version: "0.0.1", Binaries: make(map[string]Binary, len(bodies))}

	for a, body := range bodies {
		mux.HandleFunc("/echod-"+a, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
		sum := sha256.Sum256(body)
		m.Binaries[a] = Binary{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	for a, b := range m.Binaries {
		b.URL = srv.URL + "/echod-" + a
		m.Binaries[a] = b
	}
	return m
}

// one is the single build of a release that carries nothing else, for the tests about fetching rather
// than about choosing.
func one(t *testing.T, body []byte) Binary {
	t.Helper()

	b, err := release(t, map[string][]byte{arch: body}).For(arch)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDownloadChecksWhatArrived(t *testing.T) {
	dir := t.TempDir()
	b := one(t, []byte("a new echod"))

	for name, tc := range map[string]struct {
		break_ func(*Binary)
		want   string
	}{
		"as offered":   {func(*Binary) {}, ""},
		"wrong hash":   {func(b *Binary) { b.SHA256 = strings.Repeat("a", 64) }, "hash"},
		"wrong size":   {func(b *Binary) { b.Size = 4 }, "bytes"},
		"gone away":    {func(b *Binary) { b.URL += "/missing" }, "404"},
		"no such host": {func(b *Binary) { b.URL = "http://127.0.0.1:1/echod" }, "fetching"},
	} {
		offered := b
		tc.break_(&offered)

		to := filepath.Join(dir, name)
		err := download(context.Background(), offered, to, nil)

		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.want != "" && err == nil:
			t.Errorf("%s: accepted a download it should have refused", name)
		case tc.want != "" && !strings.Contains(err.Error(), tc.want):
			t.Errorf("%s: %v, want something about %q", name, err, tc.want)
		}
	}
}

// A download that was refused must not be left behind looking like a binary somebody could run.
func TestInstallRefusesAManifestItCannotUse(t *testing.T) {
	somewhere(t)

	good := Binary{URL: "http://example/echod", SHA256: strings.Repeat("a", 64), Size: 1}

	for name, m := range map[string]Manifest{
		"no version":  {Binaries: map[string]Binary{arch: good}},
		"no binaries": {Version: "0.0.1"},
		"no url":      {Version: "0.0.1", Binaries: map[string]Binary{arch: {SHA256: good.SHA256, Size: 1}}},
		"no hash":     {Version: "0.0.1", Binaries: map[string]Binary{arch: {URL: good.URL, Size: 1}}},
		"no size":     {Version: "0.0.1", Binaries: map[string]Binary{arch: {URL: good.URL, SHA256: good.SHA256}}},
	} {
		if err := Install(context.Background(), m, nil); err == nil {
			t.Errorf("%s: installed something unusable", name)
		}
	}
}

// Progress is what somebody watches instead of wondering whether it has stalled, so it has to reach 1
// and never run past it.
func TestProgressReachesTheEnd(t *testing.T) {
	dir := t.TempDir()
	b := one(t, []byte(strings.Repeat("x", 64<<10)))

	var last float32
	var calls int
	err := download(context.Background(), b, filepath.Join(dir, "echod"), func(at float32) {
		calls++
		if at < last {
			t.Errorf("progress went backwards: %v then %v", last, at)
		}
		if at > 1 {
			t.Errorf("progress reported %v", at)
		}
		last = at
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Error("nothing was reported")
	}
	if last < 0.9 {
		t.Errorf("finished at %v", last)
	}
}

// The staged file is what gets copied into /system, so it has to be exactly what was offered.
func TestDownloadWritesWhatItVerified(t *testing.T) {
	dir := t.TempDir()
	want := []byte("a new echod")
	b := one(t, want)

	to := filepath.Join(dir, "echod")
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(to)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("staged %q, want %q", got, want)
	}
}

// A manifest naming a size is what the room check is made against, so a server that keeps sending past
// it has to be cut off rather than left to write until the flash is full. The staged file proves it:
// nothing beyond the offered length plus the byte that catches the lie is ever on disk.
func TestDownloadStopsAtTheOfferedSize(t *testing.T) {
	dir := t.TempDir()

	var served int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := make([]byte, 32<<10)
		for range 64 {
			n, err := w.Write(chunk)
			served += int64(n)
			if err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	// Offered as a kilobyte; two megabytes are waiting behind it.
	b := Binary{URL: srv.URL + "/echod", SHA256: strings.Repeat("a", 64), Size: 1 << 10}

	to := filepath.Join(dir, "echod")
	err := download(context.Background(), b, to, nil)
	if err == nil {
		t.Fatal("a download that ran past its offered size was accepted")
	}
	if !strings.Contains(err.Error(), "bytes") {
		t.Errorf("%v, want something about how many bytes arrived", err)
	}

	st, statErr := os.Stat(to)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if st.Size() > b.Size+1 {
		t.Errorf("wrote %d bytes for a download offered as %d", st.Size(), b.Size)
	}
}

// An old manifest is signed as well as a new one, so the only thing standing between a device and a
// published version with a known hole is that it will not go backwards.
func TestInstallRefusesAnOlderVersionThanTheOneRunning(t *testing.T) {
	somewhere(t)

	// Stand where a released device stands: a stamped version, with the manifest offering the release
	// before it.
	running := layout.Version
	t.Cleanup(func() { layout.Version = running })
	layout.Version = "v0.7.13"

	b := Binary{URL: "http://example/echod", SHA256: strings.Repeat("a", 64), Size: 1}
	m := Manifest{Version: "v0.7.9", Binaries: map[string]Binary{arch: b}}

	err := Install(context.Background(), m, nil)
	if err == nil {
		t.Fatal("an older release was installed over a newer one")
	}
	if !strings.Contains(err.Error(), "older") {
		t.Errorf("%v, want something about the version being older", err)
	}
}

// The rule the device applies has to be the one Home Assistant's card shows, or a device refuses what
// the card offered. AwesomeVersion's rules: numbers as numbers, a prerelease below its own release,
// and nothing after the underscore counted at all.
func TestVersionsRankTheWayHomeAssistantRanksThem(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v0.7.13", "v0.7.9", 1, true},
		{"v0.7.9", "v0.7.13", -1, true},
		{"0.5.10", "v0.5.10", 0, true},
		{"v0.5.10_20260922", "v0.5.10_20260820", 0, true}, // build detail is not a version
		{"v0.8.0", "v0.8.0-beta.4", 1, true},
		{"v0.8.0-beta.4", "v0.8.0", -1, true},
		{"v0.8.0-beta.10", "v0.8.0-beta.9", 1, true},
		{"v1.0.0", "v0.99.99", 1, true},
		{"v0.5", "v0.5.0", 0, true},
		{"dev", "v0.5.10", 0, false},
		{"dot-v0.5.10", "v0.5.10", 0, false},
		{"", "v0.5.10", 0, false},
	} {
		got, ok := compareVersions(tc.a, tc.b)
		if ok != tc.ok {
			t.Errorf("%q against %q: rankable %v, want %v", tc.a, tc.b, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("%q against %q: %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// A version neither side can rank is not treated as older: an unusual stamp on a hand-built daemon
// should not be a device that can never be updated again.
func TestAnUnrankableVersionIsNotTakenForADowngrade(t *testing.T) {
	if err := notOlder("v0.5.10", "a build of my own"); err != nil {
		t.Errorf("an unrankable running version blocked an install: %v", err)
	}
	if err := notOlder("v0.5.10", "v0.5.10"); err != nil {
		t.Errorf("reinstalling the running version was refused: %v", err)
	}
	if err := notOlder("v0.5.9", "v0.5.10"); err == nil {
		t.Error("a downgrade was allowed")
	}
}
