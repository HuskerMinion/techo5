package addons

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
)

// plexRig is a running supervisor whose catalog lists one add-on with one version.
func plexRig(t *testing.T) (*rig, catalog.Version) {
	r := newRig(t)
	v := r.version("techo5-plex", "0.1.0", nil)
	r.serve(catalog.Catalog{Format: 1, Serial: 1, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	r.run()
	return r, v
}

var plexRules = []string{
	"-p tcp --dport 32500 -j ACCEPT",
	"-p tcp --dport 32501 -j ACCEPT",
	"-p udp --dport 32410:32414 -j ACCEPT",
}

// Nothing is installed without the box: the setup page cannot send an install the owner did not agree
// to, and nothing else can ask for one.
func TestInstallNeedsTheBox(t *testing.T) {
	r, _ := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), false); !errors.Is(err, ErrNotAgreed) {
		t.Fatalf("an install without the box: %v", err)
	}
	if exists(r.dir("techo5-plex")) || r.procs.count() != 0 {
		t.Error("something was installed or run")
	}
}

// An install puts the add-on in its folder, runs it as its own user with the network group and its
// state folder as home, and opens exactly its ports, at the top of INPUT, in both families.
func TestInstallRunsItAsItsOwnUserWithItsPorts(t *testing.T) {
	r, _ := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)

	dir := r.dir("techo5-plex")
	for _, f := range []string{"bin/techo5-plex", "addon.json", "installed.json", "uid", "state"} {
		if !exists(filepath.Join(dir, f)) {
			t.Errorf("%s is missing", f)
		}
	}
	if st, err := os.Stat(filepath.Join(dir, "bin", "techo5-plex")); err != nil || st.Mode().Perm()&0o111 == 0 {
		t.Errorf("the program is not executable: %v", err)
	}
	if exists(filepath.Join(r.m.root, ".incoming-techo5-plex")) {
		t.Error("the staging folder was left behind")
	}

	sp := r.procs.last().spec
	if sp.Cred == nil || sp.Cred.UID != firstUID || sp.Cred.GID != firstUID || !slices.Contains(sp.Cred.Groups, aidInet) {
		t.Errorf("runs as %+v, want its own user in the network group", sp.Cred)
	}
	state := filepath.Join(dir, "state")
	if sp.Path != filepath.Join(dir, "bin", "techo5-plex") || strings.Join(sp.Args, " ") != "-data "+state || sp.Dir != state {
		t.Errorf("started as %s %q in %s", sp.Path, sp.Args, sp.Dir)
	}
	if !slices.Contains(sp.Env, "HOME="+state) || slices.ContainsFunc(sp.Env, func(e string) bool { return strings.HasPrefix(e, "TECHO5_SIGN") }) {
		t.Errorf("environment: %q", sp.Env)
	}
	if r.owned[state] != firstUID {
		t.Errorf("the state folder was not handed to the add-on's user: %v", r.owned)
	}

	for _, tool := range tools {
		if got := r.tables.rules(tool, chain); !slices.Equal(got, plexRules) {
			t.Errorf("%s %s: %q", tool, chain, got)
		}
		if in := r.tables.rules(tool, "INPUT"); len(in) == 0 || in[0] != "-i wlan0 -j "+chain {
			t.Errorf("%s INPUT: %q, want the add-ons' jump first", tool, in)
		}
	}

	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err == nil {
		t.Error("installed twice")
	}
}

// A binary that is not the one the catalog pins is refused, and nothing of it stays.
func TestAWrongHashIsRefused(t *testing.T) {
	r := newRig(t)
	v := r.version("techo5-plex", "0.1.0", nil)
	// The same size, so it is the hash that refuses it, not a short download tried again.
	r.assets.put("/techo5-plex/0.1.0/arm", []byte("program techo5-plex 6.6.6"))
	r.serve(catalog.Catalog{Format: 1, Serial: 1, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	r.run()
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err == nil {
		t.Fatal("a binary with the wrong hash was installed")
	}
	if exists(filepath.Join(r.dir("techo5-plex"), "installed.json")) || r.procs.count() != 0 {
		t.Error("something of it was installed or run")
	}
	if p := r.m.LastProblem(); p == "" {
		t.Error("the card is not told why")
	}
}

// A manifest whose hash matches but which asks for more than its catalog entry is refused: the card
// showed the entry's ports, and those are all that may be opened.
func TestAManifestAskingMoreThanItsEntryIsRefused(t *testing.T) {
	r := newRig(t)
	v := r.version("techo5-plex", "0.1.0", nil)
	wider := v
	wider.Ports = append(slices.Clone(v.Ports), catalog.Port{Proto: "tcp", Port: 40000, Why: "more"})
	v = r.version("techo5-plex", "0.1.0", manifestFor("techo5-plex", wider))
	r.serve(catalog.Catalog{Format: 1, Serial: 1, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	r.run()
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err == nil || !strings.Contains(err.Error(), "ports") {
		t.Fatalf("installed with a manifest asking for more: %v", err)
	}
}

// Only what the catalog lists, for this device, is installed.
func TestOnlyWhatTheCatalogListsForThisDevice(t *testing.T) {
	r := newRig(t)
	v := r.version("techo5-plex", "0.1.0", nil)
	gone := r.version("techo5-plex", "0.0.9", nil)
	gone.Withdrawn = "it broke things"
	dot := r.version("dot-only", "1.0.0", nil)
	dot.MinTECHO5 = map[string]string{"dot": "v0.5.0"}
	unpinned := r.version("unpinned", "1.0.0", nil)
	unpinned.Manifest = nil
	r.serve(catalog.Catalog{Format: 1, Serial: 1, Addons: []catalog.Addon{
		entry("techo5-plex", v, gone), entry("dot-only", dot), entry("unpinned", unpinned)}})
	r.run()
	for _, c := range [][2]string{{"techo5-plex", "0.0.9"}, {"techo5-plex", "9.9.9"}, {"nothing", "1.0.0"}, {"dot-only", "1.0.0"}, {"unpinned", "1.0.0"}} {
		if err := r.m.Install(context.Background(), c[0], c[1], r.shown(c[0], c[1]), true); err == nil {
			t.Errorf("%s %s was installed", c[0], c[1])
		}
	}
	avail := map[string]Listing{}
	for _, l := range r.m.Available() {
		avail[l.Addon.ID] = l
	}
	if l := avail["techo5-plex"]; !l.Runs || l.Version.Version != "0.1.0" {
		t.Errorf("plex: %+v", l)
	}
	if l := avail["dot-only"]; l.Runs || !strings.Contains(l.Why, "kind of device") {
		t.Errorf("a Dot's add-on on a Show: runs %v, %q", l.Runs, l.Why)
	}
	if l := avail["unpinned"]; l.Runs || !strings.Contains(l.Why, "manifest") {
		t.Errorf("unpinned: runs %v, %q", l.Runs, l.Why)
	}
}

// An add-on asking for more memory than the device can spare is refused before anything is fetched.
func TestNotEnoughMemory(t *testing.T) {
	r := newRig(t)
	v := r.version("big", "1.0.0", nil)
	v.MemoryMB = 400
	v = func() catalog.Version {
		m := r.assets.put("/big/1.0.0/addon.json", manifestFor("big", v))
		v.Manifest = &m
		return v
	}()
	r.serve(catalog.Catalog{Format: 1, Serial: 1, Addons: []catalog.Addon{entry("big", v)}})
	r.run()
	if l := r.m.Available(); len(l) != 1 || l[0].Runs || !strings.Contains(l[0].Why, "memory") {
		t.Errorf("listed as %+v", l)
	}
	if err := r.m.Install(context.Background(), "big", "1.0.0", r.shown("big", "1.0.0"), true); err == nil || !strings.Contains(err.Error(), "memory") {
		t.Errorf("installed: %v", err)
	}
}

// Two add-ons never share a port: the second asking for the first's is refused.
func TestPortsAreNotShared(t *testing.T) {
	r := newRig(t)
	a := r.version("first", "1.0.0", nil)
	b := r.version("second", "1.0.0", nil)
	r.serve(catalog.Catalog{Format: 1, Serial: 1, Addons: []catalog.Addon{entry("first", a), entry("second", b)}})
	r.run()
	if err := r.m.Install(context.Background(), "first", "1.0.0", r.shown("first", "1.0.0"), true); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Install(context.Background(), "second", "1.0.0", r.shown("second", "1.0.0"), true); err == nil || !strings.Contains(err.Error(), "port") {
		t.Errorf("a second add-on on the same ports: %v", err)
	}
}

// A root add-on runs as root, and the card is told so.
func TestARootAddonRunsAsRoot(t *testing.T) {
	r := newRig(t)
	v := r.version("vpn", "1.0.0", nil)
	v.Root = true
	v.Ports = []catalog.Port{{Proto: "udp", Port: 51820, Why: "tunnel"}}
	m := r.assets.put("/vpn/1.0.0/addon.json", manifestFor("vpn", v))
	v.Manifest = &m
	r.serve(catalog.Catalog{Format: 1, Serial: 1, Addons: []catalog.Addon{entry("vpn", v)}})
	r.run()
	if l := r.m.Available(); len(l) != 1 || !l[0].AsRoot {
		t.Errorf("not listed as root: %+v", l)
	}
	if err := r.m.Install(context.Background(), "vpn", "1.0.0", r.shown("vpn", "1.0.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("vpn", StateRunning)
	if c := r.procs.last().spec.Cred; c != nil {
		t.Errorf("a root add-on runs as %+v", c)
	}
	if !r.state("vpn").AsRoot {
		t.Error("the status does not say root")
	}
}

// Where the daemon cannot run another user, an add-on would run as the daemon does, and is listed as
// root so the warning says so.
func TestNoDroppingIsSaidAsRoot(t *testing.T) {
	r, _ := plexRig(t)
	canDrop = func() bool { return false }
	if l := r.m.Available(); len(l) != 1 || !l[0].AsRoot {
		t.Errorf("listed as %+v", l)
	}
}

// A crash is restarted after a backoff; a run of quick crashes is given up on, its ports closed; Start
// tries again.
func TestRestartsWithABackoffAndGivesUp(t *testing.T) {
	r, _ := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)

	// One that ran a long while is started again after the first wait.
	r.clock.Advance(time.Hour)
	r.procs.last().exit(errors.New("exit status 2"))
	r.waitState("techo5-plex", StateRestarting)
	if got := r.tables.rules(tools[0], chain); len(got) != 0 {
		t.Errorf("ports open while it is down: %q", got)
	}
	r.clock.waitPending(t, 1, restartFirst)
	r.clock.Advance(restartFirst)
	eventually(t, "the restart", func() bool { return r.procs.count() == 2 })

	// Then it crashes at once, every time.
	r.procs.mu.Lock()
	r.procs.exitAtOnce = true
	r.procs.mu.Unlock()
	r.procs.last().exit(errors.New("exit status 1"))
	waits := []time.Duration{}
	for r.state("techo5-plex").State != StateGaveUp {
		eventually(t, "a wait or giving up", func() bool {
			return r.clock.pending(time.Hour) > 0 || r.state("techo5-plex").State == StateGaveUp
		})
		if r.state("techo5-plex").State == StateGaveUp {
			break
		}
		// Advance only until this wait's restart has happened: on a loaded machine the restarted
		// process can stop and the next wait be armed before the clock is looked at again, and
		// advancing on through it would count two waits as one.
		before, n := r.clock.Now(), r.procs.count()
		for r.clock.pending(time.Hour) > 0 && r.procs.count() == n {
			r.clock.Advance(time.Second)
		}
		eventually(t, "the restart", func() bool {
			return r.procs.count() > n || r.state("techo5-plex").State == StateGaveUp
		})
		waits = append(waits, r.clock.Now().Sub(before))
	}
	if len(waits) != giveUpAfter-1 {
		t.Errorf("gave up after %d restarts, want %d", len(waits), giveUpAfter-1)
	}
	for i := 1; i < len(waits); i++ {
		if waits[i] <= waits[i-1] && waits[i] < restartMost {
			t.Errorf("the waits do not back off: %v", waits)
		}
	}
	if !strings.Contains(r.state("techo5-plex").Problem, "Start") {
		t.Errorf("the card is not told how to try again: %q", r.state("techo5-plex").Problem)
	}
	for _, tool := range tools {
		if got := r.tables.rules(tool, chain); len(got) != 0 {
			t.Errorf("ports open after giving up: %q", got)
		}
	}

	r.procs.mu.Lock()
	r.procs.exitAtOnce = false
	r.procs.mu.Unlock()
	n := r.procs.count()
	if err := r.m.Start("techo5-plex"); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	if r.procs.count() != n+1 {
		t.Error("Start did not start it again")
	}
}

// Stop leaves it off with its ports closed, across a restart of the daemon; Start puts it back.
func TestStopStaysStopped(t *testing.T) {
	r, _ := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	if err := r.m.Stop("techo5-plex"); err != nil {
		t.Fatal(err)
	}
	if s := r.state("techo5-plex"); s.State != StateStopped || s.Enabled {
		t.Errorf("after Stop: %+v", s)
	}
	if got := r.tables.rules(tools[0], chain); len(got) != 0 {
		t.Errorf("ports open while stopped: %q", got)
	}

	// The daemon restarts: the add-on stays off.
	r.stopRun()
	root := r.m.root
	r.m = newManager(root, r.clock, r.tables.run)
	r.m.download, r.m.device = r.assets.download, func() catalog.Device {
		return catalog.Device{Family: "show", Board: "cronos", Running: "v1.6.60", Arch: "arm"}
	}
	n := r.procs.count()
	r.run()
	if s := r.state("techo5-plex"); s.ID == "" || s.State != StateStopped {
		t.Errorf("after a restart of the daemon: %+v", s)
	}
	if r.procs.count() != n {
		t.Error("a stopped add-on was started by the daemon")
	}
	if err := r.m.Start("techo5-plex"); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
}

// The firewall's jump and rules come back after something flushes them, as the Dot's firewall does to
// INPUT when it is run again.
func TestTheFirewallRulesComeBack(t *testing.T) {
	r, _ := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	r.tables.flushInput()
	_ = r.tables.run(tools[1], "-F", chain)
	if repaired, _ := r.m.fw.check(); !repaired {
		t.Error("nothing was said to be missing")
	}
	for _, tool := range tools {
		if got := r.tables.rules(tool, chain); !slices.Equal(got, plexRules) {
			t.Errorf("%s %s after the repair: %q", tool, chain, got)
		}
		if in := r.tables.rules(tool, "INPUT"); len(in) == 0 || in[0] != "-i wlan0 -j "+chain {
			t.Errorf("%s INPUT after the repair: %q", tool, in)
		}
	}
	if repaired, _ := r.m.fw.check(); repaired {
		t.Error("a second check found something missing")
	}
}

// A firewall that cannot be changed does not stop the add-on; the card says why its ports may not be
// open.
func TestAFirewallThatFailsIsSaid(t *testing.T) {
	r, _ := plexRig(t)
	r.tables.broken[tools[1]] = true
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	if s := r.state("techo5-plex"); s.Firewall == "" {
		t.Error("the firewall's failure is not said")
	}
}

// Remove deletes the folder and closes the ports; keeping the state keeps the folder that holds it and
// the user that owns it, which the next install takes up again.
func TestRemove(t *testing.T) {
	r, _ := plexRig(t)
	ctx := context.Background()
	if err := r.m.Install(ctx, "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	state := filepath.Join(r.dir("techo5-plex"), "state")
	if err := os.WriteFile(filepath.Join(state, "state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Remove("techo5-plex", true); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(state, "state.json")) || exists(filepath.Join(r.dir("techo5-plex"), "bin")) {
		t.Error("keeping the state kept the wrong things")
	}
	if len(r.m.Installed()) != 0 || len(r.tables.rules(tools[0], chain)) != 0 {
		t.Error("still installed, or its ports still open")
	}

	// Another add-on installed meanwhile does not take the kept state's user.
	other := r.version("other", "1.0.0", nil)
	other.Ports = []catalog.Port{{Proto: "tcp", Port: 40000, Why: "x"}}
	m := r.assets.put("/other/1.0.0/addon.json", manifestFor("other", other))
	other.Manifest = &m
	v := r.version("techo5-plex", "0.1.0", nil)
	r.serve(catalog.Catalog{Format: 1, Serial: 2, Addons: []catalog.Addon{entry("techo5-plex", v), entry("other", other)}})
	if err := r.m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Install(ctx, "other", "1.0.0", r.shown("other", "1.0.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("other", StateRunning)
	if c := r.procs.last().spec.Cred; c.UID == firstUID {
		t.Error("a new add-on took the kept state's user")
	}
	if err := r.m.Install(ctx, "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	if c := r.procs.last().spec.Cred; c.UID != firstUID {
		t.Errorf("reinstalled as %d, not the kept state's user", c.UID)
	}
	if !exists(filepath.Join(state, "state.json")) {
		t.Error("the kept state did not survive the reinstall")
	}

	if err := r.m.Remove("techo5-plex", false); err != nil {
		t.Fatal(err)
	}
	if exists(r.dir("techo5-plex")) {
		t.Error("the folder is still there")
	}
}

// updateRig has 0.1.0 installed and running, and the catalog then offers next.
func updateRig(t *testing.T, next func(r *rig) catalog.Version) (*rig, catalog.Version) {
	r, v1 := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	v2 := next(r)
	r.serve(catalog.Catalog{Format: 1, Serial: 2, Addons: []catalog.Addon{entry("techo5-plex", v1, v2)}})
	if err := r.m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r, v2
}

func sameAsks(r *rig) catalog.Version { return r.version("techo5-plex", "0.2.0", nil) }

// An update that stays up a minute is kept; the old version is deleted then, and the state is the same
// folder throughout.
func TestAnUpdateThatStaysUpIsKept(t *testing.T) { updateStaysUp(t, false) }

// The same, with the new version's ports slow to open, as on a loaded machine: its process is there
// before the supervisor marks it running, and the update's trial can end in between.
func TestAnUpdateThatStaysUpIsKeptWhileItsPortsOpen(t *testing.T) { updateStaysUp(t, true) }

func updateStaysUp(t *testing.T, slowFirewall bool) {
	r, _ := updateRig(t, sameAsks)
	if slowFirewall {
		r.tables.mu.Lock()
		r.tables.slow = 20 * time.Millisecond
		r.tables.mu.Unlock()
	}
	if s := r.state("techo5-plex"); s.Update == nil || s.Update.Version != "0.2.0" || len(s.Update.More) != 0 {
		t.Fatalf("offered: %+v", s.Update)
	}
	done := make(chan error, 1)
	go func() { done <- r.m.Update(context.Background(), "techo5-plex", r.offered("techo5-plex"), false, true) }()
	eventually(t, "the new version to start", func() bool {
		p := r.procs.last()
		b, _ := os.ReadFile(p.spec.Path)
		return r.procs.count() == 2 && string(b) == "program techo5-plex 0.2.0"
	})
	r.clock.waitPending(t, 1, trialFor)
	r.clock.Advance(trialFor)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The trial is timed from the start, not from running: the new process is there (and the test's
	// wait above saw it) a moment before its ports are open and it is marked running, which on a
	// loaded machine can come after the trial's end. That moment is milliseconds against a trial of a
	// minute on a device, so the test waits for it rather than the code holding the trial for it.
	r.waitState("techo5-plex", StateRunning)
	s := r.state("techo5-plex")
	if s.Version != "0.2.0" || s.Update != nil || s.State != StateRunning {
		t.Errorf("after the update: %+v", s)
	}
	if exists(filepath.Join(r.dir("techo5-plex"), ".old")) || exists(filepath.Join(r.dir("techo5-plex"), ".new")) {
		t.Error("the old or the staged version was left behind")
	}
}

// One that stops within its minute is put back, and that is said.
func TestAnUpdateThatDoesNotStayUpIsPutBack(t *testing.T) {
	r, _ := updateRig(t, sameAsks)
	done := make(chan error, 1)
	go func() {
		done <- r.m.Update(context.Background(), "techo5-plex", r.offered("techo5-plex"), false, false)
	}()
	eventually(t, "the new version to start", func() bool { return r.procs.count() == 2 })
	r.waitState("techo5-plex", StateRunning)
	r.procs.last().exit(errors.New("exit status 1"))
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "did not stay up") {
		t.Fatalf("the update: %v", err)
	}
	r.waitState("techo5-plex", StateRunning)
	b, _ := os.ReadFile(r.procs.last().spec.Path)
	if string(b) != "program techo5-plex 0.1.0" {
		t.Errorf("running %q after going back", b)
	}
	s := r.state("techo5-plex")
	if s.Version != "0.1.0" || s.RolledBack != "0.2.0" {
		t.Errorf("after going back: %+v", s)
	}
	if st := entityState(s); !strings.Contains(st.ReleaseSummary, "did not stay up") {
		t.Errorf("Home Assistant is not told: %q", st.ReleaseSummary)
	}
}

// An update that asks for more (a new port, root) needs the box again, and is never installed from
// Home Assistant, which says to use the setup page.
func TestAnUpdateAskingForMoreAsksAgain(t *testing.T) {
	r, _ := updateRig(t, func(r *rig) catalog.Version {
		v := r.version("techo5-plex", "0.2.0", nil)
		v.Ports = append(v.Ports, catalog.Port{Proto: "tcp", Port: 32600, Why: "more"})
		m := r.assets.put("/techo5-plex/0.2.0/addon.json", manifestFor("techo5-plex", v))
		v.Manifest = &m
		return v
	})
	s := r.state("techo5-plex")
	if s.Update == nil || !slices.Equal(s.Update.More, []string{"port 32600/tcp"}) {
		t.Fatalf("offered: %+v", s.Update)
	}
	if st := entityState(s); st.LatestVersion != "0.2.0" || !strings.Contains(st.ReleaseSummary, "setup page") {
		t.Errorf("Home Assistant's entity: %+v", st)
	}
	if err := r.m.Update(context.Background(), "techo5-plex", r.offered("techo5-plex"), true, true); !errors.Is(err, ErrAsksMore) {
		t.Errorf("from Home Assistant: %v", err)
	}
	if err := r.m.Update(context.Background(), "techo5-plex", r.offered("techo5-plex"), false, false); !errors.Is(err, ErrNotAgreed) {
		t.Errorf("without the box: %v", err)
	}
	if r.procs.count() != 1 {
		t.Error("something was started")
	}
	done := make(chan error, 1)
	go func() { done <- r.m.Update(context.Background(), "techo5-plex", r.offered("techo5-plex"), true, false) }()
	eventually(t, "the new version to start", func() bool { return r.procs.count() == 2 })
	r.clock.waitPending(t, 1, trialFor)
	r.clock.Advance(trialFor)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Its ports are opened as it is marked running, which can come after the trial ends (updateStaysUp).
	r.waitState("techo5-plex", StateRunning)
	if got := r.tables.rules(tools[0], chain); !slices.Contains(got, "-p tcp --dport 32600 -j ACCEPT") {
		t.Errorf("the new port is not open: %q", got)
	}
}

// A withdrawn listing is said on the card and in Home Assistant, offers nothing, and the add-on is not
// removed by itself.
func TestAWithdrawnListing(t *testing.T) {
	r, v := plexRig(t)
	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	e := entry("techo5-plex", v)
	e.Withdrawn = "its author stopped keeping it"
	r.serve(catalog.Catalog{Format: 1, Serial: 2, Addons: []catalog.Addon{e}})
	if err := r.m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := r.state("techo5-plex")
	if s.Withdrawn != e.Withdrawn || s.State != StateRunning {
		t.Errorf("after the withdrawal: %+v", s)
	}
	if st := entityState(s); st.LatestVersion != "0.1.0" || !strings.Contains(st.ReleaseSummary, "No longer listed") {
		t.Errorf("Home Assistant's entity: %+v", st)
	}
	if len(r.m.Available()) != 0 {
		t.Error("a withdrawn add-on is still offered")
	}
}

// The catalog is believed only signed, a kept copy only if it still verifies, and never an older one
// than the device has.
func TestTheCatalogIsSignedAndNeverGoesBack(t *testing.T) {
	r := newRig(t)
	v := r.version("techo5-plex", "0.1.0", nil)
	r.serve(catalog.Catalog{Format: 1, Serial: 5, Addons: []catalog.Addon{entry("techo5-plex", v)}})
	ctx := context.Background()
	if _, err := r.m.cat.refresh(ctx); err != nil {
		t.Fatal(err)
	}

	r.serve(catalog.Catalog{Format: 1, Serial: 4, Addons: nil})
	if _, err := r.m.cat.refresh(ctx); !errors.Is(err, errOlder) {
		t.Errorf("an older catalog: %v", err)
	}
	if c, _ := r.m.cat.get(); c.Serial != 5 {
		t.Errorf("serial %d after an older one was served", c.Serial)
	}
	if _, problem, _, _ := r.m.CatalogState(); problem == "" {
		t.Error("the card is not told the fetch failed")
	}

	r.mu.Lock()
	r.sig = strings.Repeat("A", 86) + "=="
	r.mu.Unlock()
	if _, err := r.m.cat.refresh(ctx); err == nil {
		t.Error("a catalog without a good signature was taken")
	}

	// The kept copy is read back on the next start, and refused once it has been changed.
	fresh := &catalogStore{dir: r.m.root, clock: r.clock}
	fresh.load()
	if c, ok := fresh.get(); !ok || c.Serial != 5 {
		t.Errorf("the kept catalog: %v %v", c.Serial, ok)
	}
	body := filepath.Join(r.m.root, "catalog.json")
	b, _ := os.ReadFile(body)
	if err := os.WriteFile(body, []byte(strings.Replace(string(b), `"serial":5`, `"serial":6`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := &catalogStore{dir: r.m.root, clock: r.clock}
	changed.load()
	if _, ok := changed.get(); ok {
		t.Error("a kept catalog changed on the device was believed")
	}
}

// The log is capped and rotated as it is written, and its tail reaches into the older file.
func TestTheLogIsCappedAndRotated(t *testing.T) {
	old := maxLog
	maxLog = 100
	t.Cleanup(func() { maxLog = old })
	path := filepath.Join(t.TempDir(), "log")
	l, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 30 {
		fmt.Fprintf(l, "line %02d\n", i)
	}
	l.Close()
	for _, p := range []string{path, path + ".1"} {
		st, err := os.Stat(p)
		if err != nil || st.Size() > maxLog {
			t.Errorf("%s: %v, %v", p, st, err)
		}
	}
	tail := tailLog(path, 15)
	if len(tail) != 15 || tail[14] != "line 29" {
		t.Errorf("tail: %q", tail)
	}
}

// What was put on the device by hand is found from rc.local and from what runs from userdata, never
// with its arguments, and never what TECHO5 itself runs.
func TestFindHandInstalled(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	oldRC, oldProc := RCLocal, procDir
	t.Cleanup(func() { RCLocal, procDir = oldRC, oldProc })
	RCLocal = filepath.Join(dir, "rc.local")
	procDir = filepath.Join(dir, "proc")
	if err := os.WriteFile(RCLocal, []byte(`#!/bin/sh
# techo5-plex: ports
for T in iptables-legacy ip6tables-legacy; do
	$T -A PLEX-IN -p tcp --dport 32500 -j ACCEPT
done
/data/techo5-linux/plex/techo5-plex -token SECRET >> /data/techo5-linux/plex/plex.log 2>&1 &
HOME=/data/x nohup /data/techo5-linux/netbird/netbird up --setup-key SECRET &
sleep 5
`), 0o755); err != nil {
		t.Fatal(err)
	}
	proc := func(pid, ppid int, exe string) {
		p := filepath.Join(procDir, fmt.Sprint(pid))
		_ = os.MkdirAll(p, 0o755)
		_ = os.Symlink(exe, filepath.Join(p, "exe"))
		_ = os.WriteFile(filepath.Join(p, "stat"), []byte(fmt.Sprintf("%d (a (b) c) S %d 0 0", pid, ppid)), 0o644)
	}
	proc(100, 1, "/data/techo5-linux/plex/techo5-plex")
	proc(101, 1, "/data/techo5-linux/sound-ai/techo5-sound-ai")
	proc(102, 1, "/usr/bin/dropbear")
	proc(103, 1, r.m.root+"/techo5-plex/bin/techo5-plex")
	proc(104, os.Getpid(), "/data/misc/techo5/tailscale/tailscaled")
	proc(105, 104, "/data/misc/techo5/tailscale/helper")

	got := r.m.FindHandInstalled()
	want := []Found{
		{Path: "/data/techo5-linux/netbird/netbird", RCLocal: true},
		{Path: "/data/techo5-linux/plex/techo5-plex", RCLocal: true, Running: true},
		{Path: "/data/techo5-linux/sound-ai/techo5-sound-ai", Running: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("found %+v, want %+v", got, want)
	}
	for _, f := range got {
		if strings.Contains(f.Path, "SECRET") {
			t.Error("an argument was shown")
		}
	}
}
