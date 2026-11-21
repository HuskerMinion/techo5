package wake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"
)

// imageWord puts a stand-in for one of the image's words in dir, as the image installs it, counts it as
// shipped for the length of the test, and returns a byte-identical offer of it from Home Assistant with
// how many times that offer was fetched.
func imageWord(t *testing.T, dir string) (esphome.ExternalWakeWord, *int) {
	t.Helper()

	model := []byte("stands in for okay_nabu")
	sum := sha256.Sum256(model)
	hash := hex.EncodeToString(sum[:])
	shipped[hash] = true
	t.Cleanup(func() { delete(shipped, hash) })

	if err := os.WriteFile(filepath.Join(dir, "okay_nabu.tflite"), model, 0o644); err != nil {
		t.Fatal(err)
	}
	image := `{"type":"micro","wake_word":"Okay Nabu","model":"okay_nabu.tflite","micro":{"sliding_window_size":5}}`
	if err := os.WriteFile(filepath.Join(dir, "okay_nabu.json"), []byte(image), 0o644); err != nil {
		t.Fatal(err)
	}

	fetched := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched++
		if strings.HasSuffix(r.URL.Path, ".json") {
			_, _ = w.Write([]byte(`{"type":"micro","wake_word":"Okay Nabu","model":"okay_nabu.tflite"}`))
			return
		}
		_, _ = w.Write(model)
	}))
	t.Cleanup(srv.Close)
	return esphome.ExternalWakeWord{ID: "okay_nabu", Phrase: "Okay Nabu", URL: srv.URL + "/okay_nabu.json",
		Size: uint32(len(model)), Hash: hash}, &fetched
}

// A copy of one of the image's words in Home Assistant's custom_wake_words is still the image's: it is
// not fetched, not stale, and not marked, so it stays on the approximate front end with the rest.
func TestAnImageWordOfferedByHomeAssistantStaysUnmarked(t *testing.T) {
	dir := t.TempDir()
	o, fetched := imageWord(t, dir)
	l := NewLibrary(dir)
	l.Offered([]esphome.ExternalWakeWord{o})

	m, ok := Find(l.Ensure([]string{"okay_nabu"}), "okay_nabu")
	if !ok || m.FromHomeAssistant {
		t.Fatalf("after a selection: found %v, marked %v; want the image's word, unmarked", ok, m.FromHomeAssistant)
	}
	l.Loaded([]string{"okay_nabu"})
	if stale := l.Stale(); len(stale) != 0 {
		t.Errorf("stale %v: the same file would be adopted again, and marked", stale)
	}

	if m, err := Adopt(context.Background(), dir, o); err != nil || m.FromHomeAssistant {
		t.Errorf("Adopt = marked %v, %v; want unmarked", m.FromHomeAssistant, err)
	}
	if load(dir, "okay_nabu").FromHomeAssistant {
		t.Error("the image's manifest was marked")
	}
	if *fetched != 0 {
		t.Errorf("fetched %d times, want none: the file is already here", *fetched)
	}
}

// A model adopted with no manifest, marked later, takes its phrase and languages from the offer, as one
// adopted today does, not from its id.
func TestMarkingAModelWithNoManifestTakesThePhraseFromTheOffer(t *testing.T) {
	dir := t.TempDir()
	model := []byte("this is not really a model")
	_, o := served(t, testConfig, model)
	o.Phrase = "Hey Testing Phrase"
	o.TrainedLanguages = []string{"de"}
	if err := os.WriteFile(filepath.Join(dir, "test.tflite"), model, 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Adopt(context.Background(), dir, o)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !m.FromHomeAssistant || m.Phrase != "Hey Testing Phrase" || len(m.Languages) != 1 || m.Languages[0] != "de" {
		t.Errorf("marked %v, phrase %q, languages %v; want marked, the offer's phrase and languages",
			m.FromHomeAssistant, m.Phrase, m.Languages)
	}
	if m.Path != filepath.Join(dir, "test.tflite") {
		t.Errorf("path %q, want the model beside the manifest", m.Path)
	}
}

// The models the image carries are the ones tools/fetch-inputs.py puts in it. Moving a pin there
// without moving it here would let a byte-identical offer mark the new file as Home Assistant's.
func TestShippedIsWhatTheImageCarries(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tools", "fetch-inputs.py"))
	if err != nil {
		t.Skipf("tools/fetch-inputs.py: %v", err)
	}
	sums := regexp.MustCompile(`'[^']+\.tflite':\s*'([0-9a-f]{64})'`).FindAllSubmatch(src, -1)
	if len(sums) == 0 {
		t.Fatal("no model sums found in tools/fetch-inputs.py")
	}
	want := map[string]bool{}
	for _, s := range sums {
		want[string(s[1])] = true
		if !shipped[string(s[1])] {
			t.Errorf("%s is in the image and not in shipped", s[1])
		}
	}
	for h := range shipped {
		if !want[h] {
			t.Errorf("%s is in shipped and not in the image", h)
		}
	}
}
