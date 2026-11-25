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

// The add-on catalog is taken only with the release key's signature over its domain and exactly what
// was served, and not before the clock is set; VerifyCatalog checks a kept copy the same way. A
// signature made over the bare bytes, as a firmware manifest's is, does not verify a catalog, and a
// catalog's does not verify as a manifest's.
func TestFetchSignedCatalogNeedsItsOwnSignatureAndTheClock(t *testing.T) {
	seed, pub := testKey(t)
	body := []byte(`{"serial":1}`)
	good, _ := SignCatalog(body, seed)
	sig := good
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	mux.HandleFunc("/catalog.json.sig", func(w http.ResponseWriter, _ *http.Request) {
		if sig == "" {
			http.NotFound(w, nil)
			return
		}
		_, _ = w.Write([]byte(sig))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	restoreKey, restoreClock := releaseKey, clockSet
	t.Cleanup(func() { releaseKey, clockSet = restoreKey, restoreClock })
	releaseKey = pub
	clockSet = func() bool { return true }

	got, gotSig, err := FetchSignedCatalog(context.Background(), srv.URL+"/catalog.json", 1<<10)
	if err != nil || string(got) != string(body) {
		t.Fatalf("a signed catalog: %q, %v", got, err)
	}
	if err := VerifyCatalog(got, gotSig); err != nil {
		t.Errorf("the kept copy does not verify: %v", err)
	}
	if err := VerifyCatalog([]byte(`{"serial":2}`), gotSig); err == nil {
		t.Error("a changed copy verified")
	}
	if err := verify(body, gotSig, pub); err == nil {
		t.Error("a catalog's signature verified as a manifest's")
	}
	sig, _ = Sign(body, seed)
	if _, _, err := FetchSignedCatalog(context.Background(), srv.URL+"/catalog.json", 1<<10); err == nil {
		t.Error("a manifest-style signature was taken for a catalog")
	}
	sig = ""
	if _, _, err := FetchSignedCatalog(context.Background(), srv.URL+"/catalog.json", 1<<10); err == nil {
		t.Error("an unsigned file was taken")
	}
	sig = good
	if _, _, err := FetchSignedCatalog(context.Background(), srv.URL+"/catalog.json", 4); err == nil {
		t.Error("a file over the limit was taken")
	}
	if _, _, err := FetchSignedCatalog(context.Background(), srv.URL+"/nothing.json", 1<<10); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing file: %v, want ErrNotFound", err)
	}
	clockSet = func() bool { return false }
	if _, _, err := FetchSignedCatalog(context.Background(), srv.URL+"/catalog.json", 1<<10); !errors.Is(err, ErrClock) {
		t.Errorf("fetched on an unset clock: %v", err)
	}
}
