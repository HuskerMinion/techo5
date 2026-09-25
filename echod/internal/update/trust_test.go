package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testKey(t *testing.T) (seed, pub string) {
	t.Helper()
	p, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k.Seed()), base64.StdEncoding.EncodeToString(p)
}

func TestSignatureVerifies(t *testing.T) {
	seed, pub := testKey(t)
	manifest := []byte(`{"version":"1.2.3"}`)
	sig, err := Sign(manifest, seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(manifest, []byte(sig), pub); err != nil {
		t.Errorf("a good signature was refused: %v", err)
	}
	if err := verify([]byte(`{"version":"6.6.6"}`), []byte(sig), pub); err == nil {
		t.Error("a changed manifest passed")
	}
	_, other := testKey(t)
	if err := verify(manifest, []byte(sig), other); err == nil {
		t.Error("another key's check passed")
	}
	if err := verify(manifest, []byte("not base64!"), pub); err == nil {
		t.Error("a malformed signature passed")
	}
}

// A manifest is taken from the channel only with its signature, and not at all before the clock is set.
func TestFetchNeedsTheSignatureAndTheClock(t *testing.T) {
	seed, pub := testKey(t)
	body, _ := json.Marshal(Manifest{Version: "9.9.9", Binaries: map[string]Binary{
		arch: {URL: "https://example/echod", SHA256: strings.Repeat("a", 64), Size: 1},
	}})
	good, _ := Sign(body, seed)
	sig := good
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	mux.HandleFunc("/manifest.json.sig", func(w http.ResponseWriter, _ *http.Request) {
		if sig == "" {
			http.NotFound(w, nil)
			return
		}
		_, _ = w.Write([]byte(sig))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	restoreKey, restoreClock, restoreURL := releaseKey, clockSet, channelURL
	t.Cleanup(func() { releaseKey, clockSet, channelURL = restoreKey, restoreClock, restoreURL })
	releaseKey = pub
	clockSet = func() bool { return true }
	channelURL = func(Channel) string { return srv.URL + "/manifest.json" }

	if m, err := Fetch(context.Background(), Stable); err != nil || m.Version != "9.9.9" {
		t.Fatalf("a signed manifest: %v, %v", m.Version, err)
	}
	sig = ""
	if _, err := Fetch(context.Background(), Stable); err == nil {
		t.Error("an unsigned manifest was taken")
	}
	_, otherPub := testKey(t)
	sig, releaseKey = good, otherPub
	if _, err := Fetch(context.Background(), Stable); err == nil {
		t.Error("a manifest signed by another key was taken")
	}
	releaseKey = pub
	clockSet = func() bool { return false }
	if _, err := Fetch(context.Background(), Stable); !errors.Is(err, ErrClock) {
		t.Errorf("fetched on an unset clock: %v", err)
	}
}
