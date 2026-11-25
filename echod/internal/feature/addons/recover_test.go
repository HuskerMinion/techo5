package addons

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
)

// An update the device went down in the middle of: the version still on trial goes back at the next
// start, and one already kept stays, its leftovers deleted.
func TestAnUnfinishedUpdateIsUndoneAtTheNextStart(t *testing.T) {
	for _, kept := range []bool{false, true} {
		r, _ := updateRig(t, sameAsks)
		dir := r.dir("techo5-plex")
		r.stopRun()

		// As Update leaves it between the switch and the end of the trial: the new version in place,
		// the old one in .old, and the record still naming the old one, or, once kept, the new.
		v2 := r.version("techo5-plex", "0.2.0", nil)
		old := filepath.Join(dir, ".old")
		if err := os.MkdirAll(old, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"bin", "addon.json"} {
			if err := os.Rename(filepath.Join(dir, name), filepath.Join(old, name)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := r.m.fetchVersion(context.Background(), "techo5-plex", v2, dir); err != nil {
			t.Fatal(err)
		}
		if kept {
			r.m.mu.Lock()
			rec := r.m.addons["techo5-plex"].rec
			r.m.mu.Unlock()
			rec.Version = "0.2.0"
			if err := r.m.writeRecord(dir, rec); err != nil {
				t.Fatal(err)
			}
		}

		r.m = newManager(r.m.root, r.clock, r.tables.run)
		r.m.download, r.m.device = r.assets.download, func() catalog.Device {
			return catalog.Device{Family: "show", Board: "cronos", Running: "v1.6.60", Arch: "arm"}
		}
		r.run()
		want := map[bool]string{false: "0.1.0", true: "0.2.0"}[kept]
		r.waitState("techo5-plex", StateRunning)
		b, _ := os.ReadFile(r.procs.last().spec.Path)
		if string(b) != "program techo5-plex "+want || r.state("techo5-plex").Version != want {
			t.Errorf("kept %v: running %q as %s, want %s", kept, b, r.state("techo5-plex").Version, want)
		}
		if exists(old) {
			t.Errorf("kept %v: .old is still there", kept)
		}
		// The next pass makes a rig of its own, which swaps the package's fakes in again: this one's
		// supervisor has to be stopped first, or it reads them as they are written.
		r.stopRun()
	}
}
