package addons

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

// An update of a stopped add-on leaves it stopped, and starts nothing.
func TestAnUpdateOfAStoppedAddonLeavesItStopped(t *testing.T) {
	r, _ := updateRig(t, sameAsks)
	if err := r.m.Stop("techo5-plex"); err != nil {
		t.Fatal(err)
	}
	n := r.procs.count()
	if err := r.m.Update(context.Background(), "techo5-plex", r.offered("techo5-plex"), false, false); err != nil {
		t.Fatal(err)
	}
	if s := r.state("techo5-plex"); s.Enabled || s.Version != "0.2.0" || s.State != StateStopped {
		t.Errorf("after the update: %+v", s)
	}
	if r.procs.count() != n {
		t.Error("the update started it")
	}
	if exists(filepath.Join(r.dir("techo5-plex"), ".old")) {
		t.Error(".old was left")
	}
	r.restart()
	if s := r.state("techo5-plex"); s.Enabled || s.Version != "0.2.0" {
		t.Errorf("after a restart of the daemon: %+v", s)
	}
}

// The storage check leaves an add-on alone while its update is on trial.
func TestStorageIsNotCheckedDuringATrial(t *testing.T) {
	r, _ := updateRig(t, sameAsks)
	done := make(chan error, 1)
	go func() {
		done <- r.m.Update(context.Background(), "techo5-plex", r.offered("techo5-plex"), false, false)
	}()
	eventually(t, "the trial", func() bool { j, ok := r.m.Busy(); return ok && j.What == "trying" })
	duMB = func(string) int64 { return 1 << 20 }
	r.m.checkStorage()
	if s := r.state("techo5-plex"); s.State == StateTooBig {
		t.Error("stopped during its trial")
	}
	r.clock.waitPending(t, 1, trialFor)
	r.clock.Advance(trialFor)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Processes of the add-on range's users left from before are killed when the supervisor starts, and an
// add-on's own leftovers before it starts.
func TestLeftoversAreKilledAtTheStart(t *testing.T) {
	r, _ := plexRig(t)
	r.stopRun()
	proc := func(pid int, uid uint32) {
		p := filepath.Join(procDir, fmt.Sprint(pid))
		_ = os.MkdirAll(p, 0o755)
		_ = os.WriteFile(filepath.Join(p, "status"), []byte(fmt.Sprintf("Name:\tx\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)), 0o644)
	}
	proc(300, firstUID+7) // a removed add-on's
	proc(301, 0)          // root's
	proc(302, 1000)       // somebody else's
	var mu sync.Mutex
	var killed []int
	killPID = func(pid int) error {
		mu.Lock()
		killed = append(killed, pid)
		mu.Unlock()
		return os.RemoveAll(filepath.Join(procDir, fmt.Sprint(pid)))
	}
	r.restart()
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(killed, []int{300}) {
		t.Errorf("killed %v, want only the add-on user's 300", killed)
	}
}

// An installed add-on's user, while it is switched on, is one whose loopback controller is trusted.
func TestInstalledUID(t *testing.T) {
	r, _ := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	if !r.m.InstalledUID(firstUID) || r.m.InstalledUID(firstUID+1) || r.m.InstalledUID(0) {
		t.Error("the installed add-on's user is not the one trusted")
	}
	if err := r.m.Stop("techo5-plex"); err != nil {
		t.Fatal(err)
	}
	if r.m.InstalledUID(firstUID) {
		t.Error("a stopped add-on's user is still trusted")
	}
}

// /run is swept with the other shared folders.
func TestRunIsSwept(t *testing.T) {
	old := tmpDirs
	t.Cleanup(func() { tmpDirs = old })
	if !slices.Contains(old, "/run") {
		t.Error("/run is not among the folders swept")
	}
}

// A record written whole survives being read back, and leaves nothing beside it.
func TestWriteWhole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "installed.json")
	if err := writeWhole(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeWhole(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "two" || exists(p+".new") {
		t.Errorf("read back %q", b)
	}
}
