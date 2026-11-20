package netbird

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The first start after the update turns the old switch off and deletes the downloaded client, and
// leaves the peer's identity and the management server for the add-on's migrate.
func TestTheFirstStartClearsTheClientAndKeepsThePeer(t *testing.T) {
	dir := t.TempDir()
	was := stateDir
	stateDir = filepath.Join(dir, "netbird")
	t.Cleanup(func() { stateDir = was })
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"netbird": {"on": true, "management": "https://netbird.example.com", "joined": true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config.Use(path)

	gone := []string{"netbird-0.81.4", "netbird-0.81.4.tar.gz.part", "LICENSE", "join-pending"}
	kept := []string{"state/default.json", "cli/netbird/config.json"}
	for _, f := range append(gone, kept...) {
		p := filepath.Join(stateDir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	Leftovers{}.Restore(config.Get())

	for _, f := range gone {
		if _, err := os.Stat(filepath.Join(stateDir, f)); !os.IsNotExist(err) {
			t.Errorf("%s is still there", f)
		}
	}
	for _, f := range kept {
		if _, err := os.Stat(filepath.Join(stateDir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if config.Get().Netbird.On {
		t.Error("the old switch is still on")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Netbird map[string]any `json:"netbird"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.Netbird["management"] != "https://netbird.example.com" || f.Netbird["on"] != nil {
		t.Errorf("netbird section = %v", f.Netbird)
	}

	// Nothing left to do: the next start finds nothing and says nothing.
	if clearClient() {
		t.Error("a second start found the client again")
	}
}
