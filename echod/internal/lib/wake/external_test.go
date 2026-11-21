package wake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"
)

// served stands in for Home Assistant's custom_wake_words directory, which it serves statically: the
// config at one path and the model beside it.
func served(t *testing.T, config string, model []byte) (*httptest.Server, esphome.ExternalWakeWord) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/esphome/wake_words/test.json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(config))
	})
	mux.HandleFunc("/api/esphome/wake_words/test_model.tflite", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(model)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	sum := sha256.Sum256(model)
	return srv, esphome.ExternalWakeWord{
		ID:               "test",
		Phrase:           "Hey Test",
		TrainedLanguages: []string{"en"},
		Size:             uint32(len(model)),
		Hash:             hex.EncodeToString(sum[:]),
		URL:              srv.URL + "/api/esphome/wake_words/test.json",
	}
}

const testConfig = `{"type":"micro","wake_word":"Ignored","model":"test_model.tflite",
	"micro":{"sliding_window_size":5,"feature_step_size":10}}`

func TestAdoptWritesTheModelAndItsManifest(t *testing.T) {
	dir := t.TempDir()
	model := []byte("this is not really a model")
	_, offer := served(t, testConfig, model)

	m, err := Adopt(context.Background(), dir, offer)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	if m.ID != "test" {
		t.Errorf("id = %q", m.ID)
	}
	// The offer names it, not the config: Home Assistant's own label is what the user picked.
	if m.Phrase != "Hey Test" {
		t.Errorf("phrase = %q, want the offered one", m.Phrase)
	}

	got, err := os.ReadFile(filepath.Join(dir, "test.tflite"))
	if err != nil {
		t.Fatalf("model was not written: %v", err)
	}
	if string(got) != string(model) {
		t.Error("the written model is not what was served")
	}
	if _, err := os.Stat(filepath.Join(dir, "test.json")); err != nil {
		t.Errorf("manifest was not written: %v", err)
	}
}

// A model from Home Assistant is marked as one, so it runs on the exact front end as an upload does: the
// same file woke a Dot uploaded and stayed silent from custom_wake_words. Home Assistant's config does
// not get to say either thing about itself.
func TestAdoptMarksTheModelAsHomeAssistants(t *testing.T) {
	dir := t.TempDir()
	_, offer := served(t, `{"type":"micro","wake_word":"x","model":"test_model.tflite","techo5_home_assistant":false,"techo5_uploaded":true,"micro":{}}`,
		[]byte("this is not really a model"))

	m, err := Adopt(context.Background(), dir, offer)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !m.FromHomeAssistant || m.Uploaded {
		t.Errorf("FromHomeAssistant %v, Uploaded %v; want true, false", m.FromHomeAssistant, m.Uploaded)
	}
	if again := load(dir, "test"); !again.FromHomeAssistant {
		t.Error("the mark was not kept in the manifest")
	}
}

// One adopted before the mark existed is marked when it is adopted again, without a download, and keeps
// what its manifest said.
func TestAdoptMarksOneAdoptedBefore(t *testing.T) {
	dir := t.TempDir()
	model := []byte("this is not really a model")
	srv, offer := served(t, testConfig, model)
	if err := os.WriteFile(filepath.Join(dir, "test.tflite"), model, 0o644); err != nil {
		t.Fatal(err)
	}
	old := `{"wake_word":"Hey Test","model":"test.tflite","trained_languages":["en"],"micro":{"sliding_window_size":7,"feature_step_size":10}}`
	if err := os.WriteFile(filepath.Join(dir, "test.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if load(dir, "test").FromHomeAssistant {
		t.Fatal("marked before it was adopted again")
	}
	srv.Close() // nothing is downloaded

	m, err := Adopt(context.Background(), dir, offer)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !m.FromHomeAssistant {
		t.Error("not marked")
	}
	if m.Phrase != "Hey Test" || m.Config.SlidingWindowSize != 7 {
		t.Errorf("phrase %q, window %d: the manifest was not kept", m.Phrase, m.Config.SlidingWindowSize)
	}
}

// A truncated or swapped model must be refused: running one would fail later, somewhere harder to
// read than here.
func TestAdoptRefusesAWrongHash(t *testing.T) {
	dir := t.TempDir()
	_, offer := served(t, testConfig, []byte("the model"))
	offer.Hash = "0000000000000000000000000000000000000000000000000000000000000000"

	if _, err := Adopt(context.Background(), dir, offer); err == nil {
		t.Fatal("a model with the wrong hash was adopted")
	}
	if _, err := os.Stat(filepath.Join(dir, "test.tflite")); err == nil {
		t.Error("a refused model was left on disk")
	}
}

func TestAdoptRefusesAWrongSize(t *testing.T) {
	dir := t.TempDir()
	_, offer := served(t, testConfig, []byte("the model"))
	offer.Size = 99

	if _, err := Adopt(context.Background(), dir, offer); err == nil {
		t.Fatal("a model of the wrong size was adopted")
	}
}

func TestAdoptNeedsTheConfigToNameAModel(t *testing.T) {
	dir := t.TempDir()
	_, offer := served(t, `{"type":"micro","wake_word":"Hey"}`, []byte("the model"))

	if _, err := Adopt(context.Background(), dir, offer); err == nil {
		t.Fatal("a config naming no model was accepted")
	}
}

// Have is what keeps a device from downloading the same model on every reconnect.
func TestHaveRecognisesWhatIsInstalled(t *testing.T) {
	dir := t.TempDir()
	model := []byte("this is not really a model")
	_, offer := served(t, testConfig, model)

	if Have(dir, offer) {
		t.Error("an empty directory reports having the model")
	}
	if _, err := Adopt(context.Background(), dir, offer); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !Have(dir, offer) {
		t.Error("an adopted model is not recognized")
	}

	// The same id offered with different contents is a different model.
	offer.Hash = "1111111111111111111111111111111111111111111111111111111111111111"
	if Have(dir, offer) {
		t.Error("a model with another hash counts as installed")
	}
}

// The id comes from Home Assistant: it is in the offer, and it comes back in the selection that asks
// for a download. It names two files in the model directory, which the daemon reads as root, so an
// id that is not a plain file name is refused rather than trimmed down to one.
func TestAdoptRefusesAnIDThatLeavesTheModelDirectory(t *testing.T) {
	for _, id := range []string{
		"../../etc/cron.d/model",
		"/etc/cron.d/model",
		`C:\Windows\win.ini`,
		"sub/model",
		`sub\model`,
		"..",
		"",
	} {
		dir := t.TempDir()
		_, offer := served(t, testConfig, []byte("this is not really a model"))
		offer.ID = id

		if _, err := Adopt(context.Background(), dir, offer); err == nil {
			t.Errorf("%q was adopted as a model id", id)
		}
		if Have(dir, offer) {
			t.Errorf("%q was reported as installed", id)
		}

		left, _ := os.ReadDir(dir)
		if len(left) != 0 {
			t.Errorf("%q left %d files in the model directory", id, len(left))
		}
	}
}
