package addons

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// withFiles is v bringing extra files beside its arm build, served with these contents, and its
// manifest naming them (or, with names, naming those instead).
func (r *rig) withFiles(id string, v catalog.Version, contents map[string]string, names ...string) catalog.Version {
	v.Files, v.Requires = map[string][]catalog.File{}, []string{"files"}
	var listed []string
	for _, name := range slices.Sorted(maps.Keys(contents)) {
		pin := r.assets.put("/"+id+"/"+v.Version+"/"+name, []byte(contents[name]))
		v.Files["arm"] = append(v.Files["arm"], catalog.File{Name: name, Binary: pin})
		listed = append(listed, name)
	}
	if names != nil {
		listed = names
	}
	var man catalog.Manifest
	_ = json.Unmarshal(manifestFor(id, v), &man)
	man.Files = listed
	b, _ := json.Marshal(man)
	m := r.assets.put("/"+id+"/"+v.Version+"/addon.json", b)
	v.Manifest = &m
	return v
}

func filesRig(t *testing.T, contents map[string]string, names ...string) (*rig, catalog.Version) {
	r := newRig(t)
	v := r.withFiles("techo5-plex", r.version("techo5-plex", "0.1.0", nil), contents, names...)
	r.serve(catalog.Catalog{Format: 2, Serial: 1, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	r.run()
	return r, v
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An install puts each extra file in bin/ beside the program, under its name, read-only, and tells the
// add-on where that folder is.
func TestInstallBringsTheExtraFiles(t *testing.T) {
	r, _ := filesRig(t, map[string]string{"netbird.tar.gz": "netbird's archive", "model.bin": "a model"})
	if ls := r.m.Available(); len(ls) != 1 || !slices.Equal(catalog.FileNames(ls[0].Files), []string{"model.bin", "netbird.tar.gz"}) {
		t.Errorf("the listing's files: %+v", ls)
	}
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	bin := filepath.Join(r.dir("techo5-plex"), "bin")
	if got := read(t, filepath.Join(bin, "netbird.tar.gz")); got != "netbird's archive" {
		t.Errorf("netbird.tar.gz holds %q", got)
	}
	if got := read(t, filepath.Join(bin, "model.bin")); got != "a model" {
		t.Errorf("model.bin holds %q", got)
	}
	for _, f := range []string{"netbird.tar.gz", "model.bin"} {
		if m := mode(t, filepath.Join(bin, f)); m != 0o644 {
			t.Errorf("%s is %v, want 0644", f, m)
		}
	}
	if m := mode(t, bin); m != 0o755 {
		t.Errorf("bin/ is %v", m)
	}
	sp := r.procs.last().spec
	if !slices.Contains(sp.Env, "TECHO5_ADDON_BIN="+bin) || !slices.Contains(sp.Env, "TECHO5_ADDON_STATE="+filepath.Join(r.dir("techo5-plex"), "state")) {
		t.Errorf("environment: %q", sp.Env)
	}
	b, _ := os.ReadFile(filepath.Join(r.dir("techo5-plex"), "installed.json"))
	var rec record
	_ = json.Unmarshal(b, &rec)
	if len(rec.Agreed.Files) != 2 {
		t.Errorf("what was agreed to: %+v", rec.Agreed)
	}

	// A file given another mode by hand is put right at the next start.
	_ = os.Chmod(filepath.Join(bin, "model.bin"), 0o666)
	r.restart()
	r.waitState("techo5-plex", StateRunning)
	if m := mode(t, filepath.Join(bin, "model.bin")); m != 0o644 {
		t.Errorf("after a restart model.bin is %v", m)
	}

	// Removing it deletes them with the program.
	if err := r.m.Remove("techo5-plex", true); err != nil {
		t.Fatal(err)
	}
	if exists(bin) {
		t.Error("bin/ and its files are still there")
	}
}

// An extra file that is not the one pinned fails the whole install, and nothing of it stays.
func TestAWrongExtraFileRefusesTheInstall(t *testing.T) {
	r := newRig(t)
	v := r.withFiles("techo5-plex", r.version("techo5-plex", "0.1.0", nil), map[string]string{"netbird.tar.gz": "netbird's archive"})
	r.assets.put("/techo5-plex/0.1.0/netbird.tar.gz", []byte("someone's archive"))
	r.serve(catalog.Catalog{Format: 2, Serial: 1, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	r.run()
	err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true)
	if err == nil || !strings.Contains(err.Error(), "netbird.tar.gz") {
		t.Fatalf("an extra file with the wrong hash: %v", err)
	}
	if exists(filepath.Join(r.dir("techo5-plex"), "installed.json")) || exists(filepath.Join(r.dir("techo5-plex"), "bin")) ||
		exists(filepath.Join(r.m.root, ".incoming-techo5-plex")) || r.procs.count() != 0 {
		t.Error("something of it was installed or run")
	}
}

// A manifest naming other extra files than its entry pins is refused before anything is downloaded
// beside it.
func TestAManifestNamingOtherFilesIsRefused(t *testing.T) {
	r, _ := filesRig(t, map[string]string{"netbird.tar.gz": "netbird's archive"}, "other.tar.gz")
	err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true)
	if err == nil || !strings.Contains(err.Error(), "extra files") {
		t.Fatalf("installed with a manifest naming other files: %v", err)
	}
	if exists(filepath.Join(r.dir("techo5-plex"), "installed.json")) {
		t.Error("it was installed")
	}
}

// The room an install needs counts its extra files.
func TestTheDiskCheckCountsExtraFiles(t *testing.T) {
	r, _ := filesRig(t, map[string]string{"netbird.tar.gz": strings.Repeat("x", 3<<20)})
	// 12 MB it says it uses, a binary under a megabyte, one to spare, and three for the file.
	freeMB = func(string) (int64, error) { return 15, nil }
	err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true)
	if err == nil || !strings.Contains(err.Error(), "16 MB") {
		t.Fatalf("with room for all but the extra file: %v", err)
	}
	freeMB = func(string) (int64, error) { return 16, nil }
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatalf("with room for it: %v", err)
	}
}

// filesUpdateRig has 0.1.0 with an extra file installed and running, and the catalog then offers
// 0.2.0 with these files.
func filesUpdateRig(t *testing.T, next map[string]string) *rig {
	r, v1 := filesRig(t, map[string]string{"netbird.tar.gz": "archive 1"})
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	v2 := r.version("techo5-plex", "0.2.0", nil)
	if next != nil {
		v2 = r.withFiles("techo5-plex", v2, next)
	}
	r.serve(catalog.Catalog{Format: 2, Serial: 2, Addons: []catalog.Addon{entry("techo5-plex", v1, v2)}})
	if err := r.m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}

// An update bringing the same file asks nothing more, and Home Assistant may install it; one with a
// changed or a new file asks again, only on the setup page.
func TestAnUpdateWithAChangedFileAsksAgain(t *testing.T) {
	same := filesUpdateRig(t, map[string]string{"netbird.tar.gz": "archive 1"})
	if s := same.state("techo5-plex"); s.Update == nil || len(s.Update.More) != 0 {
		t.Errorf("the same file: %+v", s.Update)
	}
	fewer := filesUpdateRig(t, nil)
	if s := fewer.state("techo5-plex"); s.Update == nil || len(s.Update.More) != 0 {
		t.Errorf("no file: %+v", s.Update)
	}

	r := filesUpdateRig(t, map[string]string{"netbird.tar.gz": "archive 2", "extra.bin": "more"})
	s := r.state("techo5-plex")
	if s.Update == nil || !slices.Equal(s.Update.More, []string{"the extra file extra.bin", "a changed netbird.tar.gz"}) {
		t.Fatalf("offered: %+v", s.Update)
	}
	if err := r.m.Update(context.Background(), "techo5-plex", "", true, true); !errors.Is(err, ErrAsksMore) {
		t.Errorf("from Home Assistant: %v", err)
	}
	if err := r.m.Update(context.Background(), "techo5-plex", r.offered("techo5-plex"), false, false); !errors.Is(err, ErrNotAgreed) {
		t.Errorf("without the box: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- r.m.Update(context.Background(), "techo5-plex", r.offered("techo5-plex"), true, false) }()
	eventually(t, "the new version to start", func() bool { return r.procs.count() == 2 })
	r.clock.waitPending(t, 1, trialFor)
	r.clock.Advance(trialFor)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(r.dir("techo5-plex"), "bin")
	if read(t, filepath.Join(bin, "netbird.tar.gz")) != "archive 2" || read(t, filepath.Join(bin, "extra.bin")) != "more" {
		t.Error("the new version's files are not in place")
	}
	b, _ := os.ReadFile(filepath.Join(r.dir("techo5-plex"), "installed.json"))
	var rec record
	_ = json.Unmarshal(b, &rec)
	if len(rec.Agreed.Files) != 2 {
		t.Errorf("what was agreed to after: %+v", rec.Agreed)
	}
}

// An update that does not stay up puts the old version's files back with its program.
func TestAnUpdateGoingBackPutsBackItsFiles(t *testing.T) {
	r := filesUpdateRig(t, map[string]string{"netbird.tar.gz": "archive 2", "extra.bin": "more"})
	done := make(chan error, 1)
	go func() { done <- r.m.Update(context.Background(), "techo5-plex", r.offered("techo5-plex"), true, false) }()
	eventually(t, "the new version to start", func() bool { return r.procs.count() == 2 })
	r.waitState("techo5-plex", StateRunning)
	r.procs.last().exit(errors.New("exit status 1"))
	if err := <-done; err == nil || !strings.Contains(err.Error(), "did not stay up") {
		t.Fatalf("the update: %v", err)
	}
	r.waitState("techo5-plex", StateRunning)
	bin := filepath.Join(r.dir("techo5-plex"), "bin")
	if got := read(t, filepath.Join(bin, "netbird.tar.gz")); got != "archive 1" {
		t.Errorf("after going back netbird.tar.gz holds %q", got)
	}
	if exists(filepath.Join(bin, "extra.bin")) {
		t.Error("the new version's file is still there")
	}
}

// An update the device went down in, with the new version's files in place: the old program and its
// files go back together at the next start.
func TestAnUnfinishedUpdateWithFilesIsUndone(t *testing.T) {
	for _, window := range []string{"on trial", "half moved in"} {
		t.Run(window, func(t *testing.T) {
			r := filesUpdateRig(t, map[string]string{"netbird.tar.gz": "archive 2"})
			dir := r.dir("techo5-plex")
			r.stopRun()
			old := filepath.Join(dir, ".old")
			_ = os.MkdirAll(old, 0o755)
			if err := os.Rename(filepath.Join(dir, "bin"), filepath.Join(old, "bin")); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(dir, "addon.json"), filepath.Join(old, "addon.json")); err != nil {
				t.Fatal(err)
			}
			c, _ := r.m.cat.get()
			a, _ := c.Find("techo5-plex")
			v2, _ := a.Version("0.2.0")
			if _, err := r.m.fetchVersion(context.Background(), "techo5-plex", v2, dir); err != nil {
				t.Fatal(err)
			}
			if window == "half moved in" {
				// The new bin/ in, its manifest not yet.
				_ = os.Remove(filepath.Join(dir, "addon.json"))
			}
			r.restart()
			r.waitState("techo5-plex", StateRunning)
			bin := filepath.Join(dir, "bin")
			if b := read(t, r.procs.last().spec.Path); b != "program techo5-plex 0.1.0" {
				t.Errorf("running %q", b)
			}
			if got := read(t, filepath.Join(bin, "netbird.tar.gz")); got != "archive 1" {
				t.Errorf("netbird.tar.gz holds %q", got)
			}
			if exists(old) {
				t.Error(".old is still there")
			}
		})
	}
}

// The room a version needs is refused, not taken as little, when its sizes are negative or would wrap
// the sum: the catalog refuses such sizes too, but this is the last check before the storage is written.
func TestRoomRefusesSizesThatDoNotAddUp(t *testing.T) {
	v := catalog.Version{DiskMB: 12, Binaries: map[string]update.Binary{"arm": {Size: 10 << 20}}}
	if need, ok := neededMB(v, "arm"); !ok || need != 12+10+1 {
		t.Errorf("a plain version: %d %v", need, ok)
	}
	for name, files := range map[string][]catalog.File{
		"negative": {{Name: "a", Binary: update.Binary{Size: -1 << 40}}},
		"wrapping": {{Name: "a", Binary: update.Binary{Size: 1 << 62}}, {Name: "b", Binary: update.Binary{Size: 1 << 62}}},
		"huge":     {{Name: "a", Binary: update.Binary{Size: 1<<63 - 1}}},
	} {
		w := v
		w.Files = map[string][]catalog.File{"arm": files}
		if _, ok := neededMB(w, "arm"); ok {
			t.Errorf("%s: taken", name)
		}
		r := newRig(t)
		if err := r.m.room(w, "arm"); err == nil {
			t.Errorf("%s: room found", name)
		}
	}
}
