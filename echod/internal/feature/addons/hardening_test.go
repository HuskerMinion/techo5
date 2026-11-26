package addons

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
)

// restart is the daemon starting again over the same folder.
func (r *rig) restart() {
	r.stopRun()
	r.m = newManager(r.m.root, r.clock, r.tables.run)
	r.m.download, r.m.device = r.assets.download, func() catalog.Device {
		return catalog.Device{Family: "show", Board: "cronos", Running: "v1.6.60", Arch: "arm"}
	}
	r.run()
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

// An add-on's user can reach its program and its state folder: every folder on the way is searchable
// by others, the program and its folder are root's and not writable by others, and the state folder is
// the add-on's alone. A folder an older build made 0750 is put right at the next start.
func TestTheFoldersLetTheAddonsUserIn(t *testing.T) {
	r, _ := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	check := func() {
		t.Helper()
		dir := r.dir("techo5-plex")
		for _, d := range []string{r.m.root, dir, filepath.Join(dir, "bin")} {
			if m := mode(t, d); m != 0o755 {
				t.Errorf("%s is %v, want 0755", d, m)
			}
		}
		if m := mode(t, filepath.Join(dir, "bin", "techo5-plex")); m != 0o755 {
			t.Errorf("the program is %v, want 0755", m)
		}
		if m := mode(t, filepath.Join(dir, "state")); m != 0o700 {
			t.Errorf("the state folder is %v, want 0700", m)
		}
		if r.owned[filepath.Join(dir, "state")] != firstUID {
			t.Error("the state folder is not the add-on's user's")
		}
	}
	check()
	_ = os.Chmod(r.dir("techo5-plex"), 0o750)
	_ = os.Chmod(filepath.Join(r.dir("techo5-plex"), "state"), 0o755)
	r.restart()
	r.waitState("techo5-plex", StateRunning)
	check()
}

// A user id once given out is never given to another add-on, removed or not.
func TestUserIDsAreNeverGivenTwice(t *testing.T) {
	r := newRig(t)
	a := r.version("first", "1.0.0", nil)
	b := r.version("second", "1.0.0", nil)
	b.Ports = []catalog.Port{{Proto: "tcp", Port: 40000, Why: "x"}}
	m := r.assets.put("/second/1.0.0/addon.json", manifestFor("second", b))
	b.Manifest = &m
	r.serve(catalog.Catalog{Format: 1, Serial: 1, Addons: []catalog.Addon{entry("first", a), entry("second", b)}})
	r.run()
	ctx := context.Background()
	if err := r.m.Install(ctx, "first", "1.0.0", r.shown("first", "1.0.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("first", StateRunning)
	if err := r.m.Remove("first", false); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Install(ctx, "second", "1.0.0", r.shown("second", "1.0.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("second", StateRunning)
	if c := r.procs.last().spec.Cred; c.UID == firstUID {
		t.Error("a removed add-on's user id was given to another")
	}
}

// Stopping or removing an add-on kills every process its user still runs, the ones that left its
// process group too, and sweeps what it left in the shared temporary folders.
func TestStopKillsEverythingTheUserRuns(t *testing.T) {
	r, _ := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	proc := func(pid int, uid uint32) {
		p := filepath.Join(procDir, fmt.Sprint(pid))
		_ = os.MkdirAll(p, 0o755)
		_ = os.WriteFile(filepath.Join(p, "status"), []byte(fmt.Sprintf("Name:\tx\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)), 0o644)
	}
	proc(200, firstUID) // left its group with setsid
	proc(201, firstUID)
	proc(202, 0) // root's: not the add-on's
	var mu sync.Mutex
	var killed []int
	killPID = func(pid int) error {
		mu.Lock()
		killed = append(killed, pid)
		mu.Unlock()
		return os.RemoveAll(filepath.Join(procDir, fmt.Sprint(pid)))
	}
	if err := r.m.Stop("techo5-plex"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	slices.Sort(killed)
	if !slices.Equal(killed, []int{200, 201}) {
		t.Errorf("killed %v, want the add-on user's 200 and 201", killed)
	}
}

// Only what the user owns is swept from the temporary folders, at their top, and nothing through a link.
func TestSweepTakesOnlyTheUsersFiles(t *testing.T) {
	dir := t.TempDir()
	old := tmpDirs
	t.Cleanup(func() { tmpDirs = old })
	tmpDirs = []string{dir}
	mine := filepath.Join(dir, "left-behind")
	_ = os.WriteFile(mine, []byte("x"), 0o600)
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	sweepUID(uint32(os.Getuid()))
	if exists(mine) || exists(filepath.Join(dir, "link")) {
		t.Error("what the user owns was not swept")
	}
	if !exists("/etc/passwd") {
		t.Fatal("a link was followed")
	}
	sweepUID(uint32(os.Getuid()) + 1)
}

// An unprivileged add-on is started through the shell with its limits set before it runs.
func TestLimitsAreSetBeforeTheProgramRuns(t *testing.T) {
	s := spec{Path: "/data/x/bin/prog", Args: []string{"-a", "$(reboot)"}, Cred: &cred{UID: 30000}, NProc: 128, AddressMB: 1536}
	path, args := limited(s)
	want := []string{"-c", "ulimit -u 128 && ulimit -v 1572864 && exec \"$0\" \"$@\"", "/data/x/bin/prog", "-a", "$(reboot)"}
	if path != shell || !slices.Equal(args, want) {
		t.Errorf("started as %s %q", path, args)
	}
	s.Cred = nil
	if path, args := limited(s); path != s.Path || !slices.Equal(args, s.Args) {
		t.Errorf("a root add-on is wrapped: %s %q", path, args)
	}
	if addressMB(40, "arm") != addressFloorMB || addressMB(200, "arm") != 3200 || addressMB(40, "arm64") != addressFloor64MB {
		t.Error("the address limits are not as documented")
	}
}

// The real spawn with limits: the shell runs, and the program starts under them. The device's /bin/sh
// is busybox's ash, whose ulimit takes -u; a workstation's may be dash, whose does not, so the test
// runs busybox where it can find it.
func TestSpawnWithLimits(t *testing.T) {
	bb, err := exec.LookPath("busybox")
	if err != nil {
		t.Skip("no busybox here")
	}
	dir := t.TempDir()
	sh := filepath.Join(dir, "sh")
	if err := os.Symlink(bb, sh); err != nil {
		t.Fatal(err)
	}
	old := shell
	t.Cleanup(func() { shell = old })
	shell = sh
	out := &syncWriter{}
	// A spec with limits but run as this user: the limit wrapper needs Cred set, so its shape is
	// checked here without changing user, which only root can.
	path, args := limited(spec{Path: sh, Args: []string{"-c", "ulimit -u; ulimit -v"}, Cred: &cred{}, NProc: 128, AddressMB: 2048})
	p, err := spawn(spec{Path: path, Args: args, Dir: t.TempDir(), Out: out})
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Wait()
	if got := strings.Fields(out.String()); !slices.Equal(got, []string{"128", "2097152"}) {
		t.Errorf("the program ran with limits %q", got)
	}
}

// Not enough memory is a failure to start like a crash: tried again after the backoff, so an add-on
// that could not start at boot does once the memory is there.
func TestNotEnoughMemoryIsTriedAgain(t *testing.T) {
	r, _ := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	var mu sync.Mutex
	have := 50
	memAvailableMB = func() (int, error) { mu.Lock(); defer mu.Unlock(); return have, nil }
	r.procs.last().exit(errors.New("killed"))
	r.clock.waitPending(t, 1, restartFirst)
	r.clock.Advance(restartFirst)
	r.waitState("techo5-plex", StateNoMemory)
	mu.Lock()
	have = 445
	mu.Unlock()
	for r.state("techo5-plex").State != StateRunning {
		r.clock.waitPending(t, 1, time.Hour)
		r.clock.Advance(restartMost)
		eventually(t, "a try", func() bool { return r.state("techo5-plex").State != StateNoMemory || r.clock.pending(time.Hour) > 0 })
	}
}

// While an update owns an add-on, Start and Stop are turned away; and the record the trial keeps is the
// add-on's as it is then.
func TestNothingElseChangesAnAddonDuringItsUpdate(t *testing.T) {
	r, _ := updateRig(t, sameAsks)
	done := make(chan error, 1)
	go func() {
		done <- r.m.Update(context.Background(), "techo5-plex", r.offered("techo5-plex"), false, false)
	}()
	eventually(t, "the trial", func() bool { j, ok := r.m.Busy(); return ok && j.What == "trying" })
	if err := r.m.Stop("techo5-plex"); !errors.Is(err, ErrBusy) {
		t.Errorf("Stop during the trial: %v", err)
	}
	if err := r.m.Remove("techo5-plex", false); !errors.Is(err, ErrBusy) {
		t.Errorf("Remove during the trial: %v", err)
	}
	r.clock.waitPending(t, 1, trialFor)
	r.clock.Advance(trialFor)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s := r.state("techo5-plex"); !s.Enabled || s.Version != "0.2.0" {
		t.Errorf("after the trial: %+v", s)
	}
}

// Both moments a power cut can fall in the middle of the switch are put right at the next start.
func TestAHalfDoneSwitchIsUndone(t *testing.T) {
	for _, window := range []string{"moving the old version aside", "moving the new version in"} {
		t.Run(window, func(t *testing.T) {
			r, _ := updateRig(t, sameAsks)
			dir := r.dir("techo5-plex")
			r.stopRun()
			old := filepath.Join(dir, ".old")
			_ = os.MkdirAll(old, 0o755)
			// The first name moved aside...
			if err := os.Rename(filepath.Join(dir, "bin"), filepath.Join(old, "bin")); err != nil {
				t.Fatal(err)
			}
			if window == "moving the new version in" {
				// ...then the second, and the new version's program in, before its manifest.
				if err := os.Rename(filepath.Join(dir, "addon.json"), filepath.Join(old, "addon.json")); err != nil {
					t.Fatal(err)
				}
				_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
				_ = os.WriteFile(filepath.Join(dir, "bin", "techo5-plex"), []byte("program techo5-plex 0.2.0"), 0o755)
			}
			r.restart()
			r.waitState("techo5-plex", StateRunning)
			b, _ := os.ReadFile(r.procs.last().spec.Path)
			if string(b) != "program techo5-plex 0.1.0" || r.state("techo5-plex").Version != "0.1.0" {
				t.Errorf("running %q as %s", b, r.state("techo5-plex").Version)
			}
			if exists(old) {
				t.Error(".old is still there")
			}
		})
	}
}

// What the box agreed to is what is installed: a listing that changed since the page is refused, and
// the card says why.
func TestAChangedListingIsNotInstalled(t *testing.T) {
	r, _ := plexRig(t)
	shown := r.shown("techo5-plex", "0.1.0")
	v := r.version("techo5-plex", "0.1.0", nil)
	v.Root = true
	m := r.assets.put("/techo5-plex/0.1.0/addon.json", manifestFor("techo5-plex", v))
	v.Manifest = &m
	r.serve(catalog.Catalog{Format: 1, Serial: 2, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	if err := r.m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", shown, true); !errors.Is(err, ErrChanged) {
		t.Errorf("an install of a changed listing: %v", err)
	}
	if !strings.Contains(r.m.LastProblem(), "changed") {
		t.Errorf("the card is not told: %q", r.m.LastProblem())
	}
}

// An expired catalog offers no install and no update; what is installed keeps running.
func TestAnExpiredCatalogOffersNothing(t *testing.T) {
	r, _ := updateRig(t, sameAsks)
	c, _ := r.m.cat.get()
	c.Expires = r.clock.Now().Add(time.Hour)
	r.serve(c)
	if err := r.m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(2 * time.Hour)
	if s := r.state("techo5-plex"); s.Update != nil || s.State != StateRunning {
		t.Errorf("after the expiry: %+v", s)
	}
	if err := r.m.Update(context.Background(), "techo5-plex", "", false, true); !errors.Is(err, ErrExpired) {
		t.Errorf("an update from an expired catalog: %v", err)
	}
	for _, l := range r.m.Available() {
		if l.Runs {
			t.Errorf("%s is offered from an expired catalog", l.Addon.ID)
		}
	}
}

// The highest serial seen outlives the kept catalog: an older signed catalog put in its place on the
// storage is not believed.
func TestTheHighestSerialIsKeptApart(t *testing.T) {
	r := newRig(t)
	v := r.version("techo5-plex", "0.1.0", nil)
	ctx := context.Background()
	r.serve(catalog.Catalog{Format: 1, Serial: 1, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	if _, err := r.m.cat.refresh(ctx); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	oldBody, oldSig := r.served, r.sig
	r.mu.Unlock()
	r.serve(catalog.Catalog{Format: 1, Serial: 2, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	if _, err := r.m.cat.refresh(ctx); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(r.m.root, "catalog.json"), oldBody, 0o644)
	_ = os.WriteFile(filepath.Join(r.m.root, "catalog.json.sig"), []byte(oldSig), 0o644)
	fresh := &catalogStore{dir: r.m.root, clock: r.clock}
	fresh.load()
	if _, ok := fresh.get(); ok {
		t.Error("an older catalog put in place of the kept one was believed")
	}
}

// A catalog that cannot be fetched is tried again less and less often, up to catalogEvery, and its
// failure is said once, not every minute.
func TestAFailingFetchBacksOff(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	var gaps []time.Duration
	var changes []bool
	for range 12 {
		for !r.m.cat.due() {
			r.clock.Advance(time.Minute)
		}
		before := r.clock.Now()
		changed, err := r.m.cat.refresh(ctx)
		if err == nil {
			t.Fatal("a fetch of nothing worked")
		}
		changes = append(changes, changed)
		r.m.cat.mu.Lock()
		gaps = append(gaps, r.m.cat.next.Sub(before))
		r.m.cat.mu.Unlock()
	}
	if gaps[0] != catalogRetry || gaps[1] != 2*catalogRetry || gaps[len(gaps)-1] != catalogEvery {
		t.Errorf("waits %v", gaps)
	}
	if !changes[0] || slices.Contains(changes[1:], true) {
		t.Errorf("said changed %v, want only the first", changes)
	}
}

// An add-on whose state folder grows well past what it said is stopped, and the card says why.
func TestTooMuchStorageStopsIt(t *testing.T) {
	r, _ := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	duMB = func(string) int64 { return 1000 }
	r.m.checkStorage()
	if s := r.state("techo5-plex"); s.State != StateTooBig || !strings.Contains(s.Problem, "MB") {
		t.Errorf("after growing: %+v", s)
	}
	if len(r.tables.rules(tools[0], chain)) != 0 {
		t.Error("its ports are still open")
	}
}

// A firewall tool that is missing for good is said once, not reported repaired every minute.
func TestABrokenFirewallIsSaidOnce(t *testing.T) {
	r, _ := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	r.tables.broken[tools[1]] = true
	repaired, broken := r.m.fw.check()
	if repaired || broken == "" {
		t.Errorf("first check: repaired %v, broken %q", repaired, broken)
	}
	if repaired, broken := r.m.fw.check(); repaired || broken != "" {
		t.Errorf("second check: repaired %v, broken %q", repaired, broken)
	}
}

// Whatever an older daemon left in the add-ons' chain goes at the start, add-ons installed or not.
func TestTheChainIsEmptiedAtTheStart(t *testing.T) {
	r := newRig(t)
	for _, tool := range tools {
		_ = r.tables.run(tool, "-N", chain)
		_ = r.tables.run(tool, "-A", chain, "-p", "tcp", "--dport", "40000", "-j", "ACCEPT")
	}
	_ = os.MkdirAll(filepath.Join(r.m.root, ".incoming-x"), 0o755)
	r.run()
	if got := r.tables.rules(tools[0], chain); len(got) != 0 {
		t.Errorf("left in the chain: %q", got)
	}
	if exists(filepath.Join(r.m.root, ".incoming-x")) {
		t.Error("a stale staging folder was left")
	}
}
