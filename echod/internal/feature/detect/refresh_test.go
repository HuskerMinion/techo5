package detect

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// hosting stands in for Home Assistant's custom_wake_words directory with one model in it, and returns
// the offer it would make for that model.
func hosting(t *testing.T, model []byte) esphome.ExternalWakeWord {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/alfred.json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"type":"micro","wake_word":"Hey Alfred","model":"alfred_model.tflite"}`))
	})
	mux.HandleFunc("/alfred_model.tflite", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(model) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	sum := sha256.Sum256(model)
	return esphome.ExternalWakeWord{ID: "alfred", Phrase: "Hey Alfred", URL: srv.URL + "/alfred.json",
		Size: uint32(len(model)), Hash: hex.EncodeToString(sum[:])}
}

// refreshing is a device running "alfred" from an old download, and counts the engines built for it:
// a model only reaches an engine by being loaded into a new one or one that does not have it yet.
func refreshing(t *testing.T) (*Detect, *int) {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alfred.tflite"), []byte("old model"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alfred.json"), []byte(`{"type":"micro","wake_word":"Hey Alfred","model":"alfred.tflite"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	fakes(t)
	built, inner := 0, build
	build = func(k wake.Kind) (backend, error) {
		built++
		return inner(k)
	}

	busy, _ := fixture(t, warm{0: true})
	d := &Detect{engine: New(2, nil), busy: busy, lib: wake.NewLibrary(dir)}
	if got := d.load([]string{"alfred"}); len(got) != 1 {
		t.Fatalf("loaded %v, want alfred", got)
	}
	return d, &built
}

// A model Home Assistant replaced under a running id is downloaded and is what the slot then runs. The
// engine keeps a slot that already has the id, so without the slot being cleared the new file sat on
// disk while the old model went on listening.
func TestRefreshSwapsInAReplacedModel(t *testing.T) {
	d, built := refreshing(t)
	model := []byte("new model")
	d.lib.Offered([]esphome.ExternalWakeWord{hosting(t, model)})

	accepted, changed := d.refreshLocked()
	if !changed || len(accepted) != 1 || accepted[0] != "alfred" {
		t.Fatalf("refresh = %v, %v; want alfred, changed", accepted, changed)
	}
	if *built != 2 {
		t.Errorf("%d engines built, want a second for the new model", *built)
	}
	if got, _ := os.ReadFile(filepath.Join(d.lib.Dir(), "alfred.tflite")); !bytes.Equal(got, model) {
		t.Errorf("on disk %q, want the new model", got)
	}
	if stale := d.lib.Stale(); len(stale) != 0 {
		t.Errorf("still stale: %v", stale)
	}
}

// A model that cannot be fetched leaves the old one listening, and is not tried on every connect after:
// only an offer with a different hash is.
func TestRefreshKeepsTheOldModelWhenTheDownloadFails(t *testing.T) {
	d, built := refreshing(t)
	o := hosting(t, []byte("new model"))
	o.Hash = "not the hash"
	d.lib.Offered([]esphome.ExternalWakeWord{o})

	if _, changed := d.refreshLocked(); changed {
		t.Error("a failed download counted as a change")
	}
	if *built != 1 || !d.engine.slots[0].loaded {
		t.Errorf("slot 1 loaded=%v after %d builds, want the old model still running", d.engine.slots[0].loaded, *built)
	}
	if stale := d.lib.Stale(); len(stale) != 0 {
		t.Errorf("stale %v right after failing; it would be fetched again on every connect", stale)
	}

	o.Hash = "another hash"
	d.lib.Offered([]esphome.ExternalWakeWord{o})
	if stale := d.lib.Stale(); len(stale) != 1 {
		t.Errorf("stale %v, want alfred again once a different model is offered", stale)
	}
}
