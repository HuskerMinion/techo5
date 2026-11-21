package detect

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// refuser is a fake engine that will not take the models named in it, as one does a model it cannot
// read, and counts the models it is handed, taken or not.
type refuser struct {
	fake
	refuse []string
	handed *int
}

func (r *refuser) load(m wake.Model) error {
	*r.handed++
	if slices.Contains(r.refuse, m.ID) {
		return errors.New("refused")
	}
	return r.fake.load(m)
}

// restoring is a device with "alfred" and "broken" on disk, nothing running, and an engine that refuses
// "broken". It counts the engines built and the models the engine was handed.
func restoring(t *testing.T) (*Detect, *int) {
	t.Helper()

	dir := t.TempDir()
	for _, id := range []string{"alfred", "broken"} {
		if err := os.WriteFile(filepath.Join(dir, id+".tflite"), []byte("old model"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	handed := 0
	was := build
	build = func(wake.Kind) (backend, error) {
		return &refuser{fake: fake{loaded: map[string]float64{}}, refuse: []string{"broken"}, handed: &handed}, nil
	}
	t.Cleanup(func() { build = was })

	busy, _ := fixture(t, warm{0: true, 1: true})
	return &Detect{engine: New(2, nil), busy: busy, lib: wake.NewLibrary(dir)}, &handed
}

func fixed(ids ...string) func() []string { return func() []string { return ids } }

// Restoring the saved selection downloads nothing, even for a word Home Assistant now offers with a
// new model: Home Assistant is waiting for the answer and gives up long before a download would. The
// word loads as it is on disk, and is left stale for the refresh after the answer.
func TestRestoreLoadsFromDiskWithoutDownloading(t *testing.T) {
	d, _ := restoring(t)
	fetched := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { fetched++ }))
	t.Cleanup(srv.Close)
	d.lib.Offered([]esphome.ExternalWakeWord{{ID: "alfred", Phrase: "Alfred", URL: srv.URL + "/alfred.json", Size: 9, Hash: "new"}})

	accepted, ok := d.restoreFrom(fixed("alfred"), fixed())
	if !ok || !slices.Equal(accepted, []string{"alfred"}) {
		t.Fatalf("restore = %v, %v; want alfred loaded", accepted, ok)
	}
	if fetched != 0 {
		t.Errorf("%d requests to Home Assistant, want none", fetched)
	}
	if stale := d.lib.Stale(); !slices.Equal(stale, []string{"alfred"}) {
		t.Errorf("stale %v, want alfred left for the refresh", stale)
	}
}

// A load or a refresh already running is not waited for: restore gives up at once.
func TestRestoreDoesNotWaitForALoad(t *testing.T) {
	d, handed := restoring(t)

	d.refreshing.Store(true)
	if _, ok := d.restoreFrom(fixed("alfred"), fixed()); ok {
		t.Error("restored during a refresh")
	}
	d.refreshing.Store(false)

	d.loading.Lock()
	if _, ok := d.restoreFrom(fixed("alfred"), fixed()); ok {
		t.Error("restored during a load")
	}
	d.loading.Unlock()
	if *handed != 0 {
		t.Errorf("the engine was handed %d models, want none", *handed)
	}
}

// A saved selection the engine will not take all of is not loaded again on every request, only once
// the selection or the models on disk change, or a selection is made.
func TestRestoreDoesNotRetryWhatTheEngineRefused(t *testing.T) {
	d, handed := restoring(t)

	accepted, ok := d.restoreFrom(fixed("alfred", "broken"), fixed())
	if !ok || !slices.Equal(accepted, []string{"alfred"}) {
		t.Fatalf("restore = %v, %v; want alfred, with broken refused", accepted, ok)
	}
	first := *handed

	if _, ok := d.restoreFrom(fixed("alfred", "broken"), fixed("alfred")); ok || *handed != first {
		t.Errorf("loaded again (%v, %d models handed after %d) for the same refusal", ok, *handed, first)
	}

	// A different selection is tried.
	if _, ok := d.restoreFrom(fixed("broken"), fixed("alfred")); !ok {
		t.Error("a different saved selection was not tried")
	}

	// The models changing is a reason to try again.
	d.restoreFrom(fixed("alfred", "broken"), fixed())
	d.lib.Reload()
	if _, ok := d.restoreFrom(fixed("alfred", "broken"), fixed("alfred")); !ok {
		t.Error("not tried again after the models changed")
	}

	// So is a selection.
	d.load([]string{"alfred"})
	if _, ok := d.restoreFrom(fixed("alfred", "broken"), fixed("alfred")); !ok {
		t.Error("not tried again after a selection")
	}
}
