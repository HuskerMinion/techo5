//go:build linux && !dot && !spot

package cpuboost

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake is a screen a test touches.
type fake struct {
	mu    sync.Mutex
	down  bool
	fns   []func(bool)
	ready chan struct{}
	once  sync.Once
}

func (f *fake) readyCh() chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ready == nil {
		f.ready = make(chan struct{})
	}
	return f.ready
}

func (f *fake) Touched() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.down
}

func (f *fake) Listen(fn func(bool)) func() {
	ch := f.readyCh()
	f.mu.Lock()
	f.fns = append(f.fns, fn)
	f.mu.Unlock()
	f.once.Do(func() { close(ch) })
	return func() {}
}

func (f *fake) set(v bool) {
	f.mu.Lock()
	f.down = v
	fns := append([]func(bool){}, f.fns...)
	f.mu.Unlock()
	for _, fn := range fns {
		fn(v)
	}
}

// board is a root with one cluster whose minimum is cur and whose maximum is top.
func board(t *testing.T, cur, top string) (root, minPath string) {
	t.Helper()
	root = t.TempDir()
	d := filepath.Join(root, cpufreqDir, "policy4")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"scaling_min_freq": cur, "cpuinfo_max_freq": top} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, filepath.Join(d, "scaling_min_freq")
}

func contents(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func waitFor(t *testing.T, path, want string) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if contents(t, path) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s holds %q, want %q", filepath.Base(path), contents(t, path), want)
}

// start runs the boost and returns once it is listening for touches.
func start(t *testing.T, root string, f *fake, after time.Duration) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = run(ctx, root, f, after); close(done) }()
	select {
	case <-f.readyCh():
	case <-time.After(5 * time.Second):
		t.Fatal("the boost never started listening")
	}
	return func() { cancel(); <-done }
}

func TestRaisedWhileTouchedAndAfterTheHold(t *testing.T) {
	root, path := board(t, "793000", "1989000")
	f := &fake{}
	stop := start(t, root, f, 300*time.Millisecond)
	defer stop()

	f.set(true)
	waitFor(t, path, "1989000")
	f.set(false)
	time.Sleep(60 * time.Millisecond)
	if got := contents(t, path); got != "1989000" {
		t.Fatalf("minimum %s inside the hold, want it kept up", got)
	}
	waitFor(t, path, "793000")
}

func TestATouchInsideTheHoldKeepsItUp(t *testing.T) {
	root, path := board(t, "793000", "1989000")
	f := &fake{}
	stop := start(t, root, f, 300*time.Millisecond)
	defer stop()

	f.set(true)
	f.set(false)
	time.Sleep(100 * time.Millisecond)
	f.set(true)
	time.Sleep(450 * time.Millisecond) // past where the first hold would have ended
	if got := contents(t, path); got != "1989000" {
		t.Fatalf("minimum %s while a finger is down, want it up", got)
	}
	f.set(false)
	waitFor(t, path, "793000")
}

func TestATapBetweenTwoLooksStillBoosts(t *testing.T) {
	root, path := board(t, "793000", "1989000")
	f := &fake{}
	stop := start(t, root, f, 300*time.Millisecond)
	defer stop()

	f.set(true)
	f.set(false) // lifted before the loop looked
	waitFor(t, path, "1989000")
	waitFor(t, path, "793000")
}

func TestPutBackWhenTheDaemonStops(t *testing.T) {
	root, path := board(t, "793000", "1989000")
	f := &fake{}
	stop := start(t, root, f, time.Hour)

	f.set(true)
	waitFor(t, path, "1989000")
	stop()
	if got := contents(t, path); got != "793000" {
		t.Fatalf("minimum %s after stopping, want 793000", got)
	}
}

func TestWhatWasThereAtTheTouchIsWhatGoesBack(t *testing.T) {
	// Somebody changed the minimum after the daemon started: that, not the value at start-up, is what
	// the boost replaced and what it puts back.
	root, path := board(t, "793000", "1989000")
	f := &fake{}
	stop := start(t, root, f, 100*time.Millisecond)
	defer stop()

	if err := os.WriteFile(path, []byte("910000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.set(true)
	waitFor(t, path, "1989000")
	f.set(false)
	waitFor(t, path, "910000")
}

func TestAMinimumSetToTheMaximumOnPurposeStays(t *testing.T) {
	root, path := board(t, "1989000", "1989000")
	f := &fake{}
	stop := start(t, root, f, 100*time.Millisecond)
	defer stop()

	if got := contents(t, path); got != "1989000" {
		t.Fatalf("minimum %s at start-up, want the one somebody set", got)
	}
	f.set(true)
	f.set(false)
	time.Sleep(250 * time.Millisecond)
	if got := contents(t, path); got != "1989000" {
		t.Fatalf("minimum %s after a touch, want the one somebody set", got)
	}
}

func TestAMinimumARaisedDaemonLeftGoesBack(t *testing.T) {
	// A daemon that died while boosted left the minimum at the maximum and its marker: the next one
	// puts back what the marker says.
	root, path := board(t, "1989000", "1989000")
	marker := filepath.Join(root, markerPath)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(path+" 793000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := start(t, root, &fake{}, 100*time.Millisecond)
	defer stop()

	if got := contents(t, path); got != "793000" {
		t.Fatalf("minimum %s at start-up, want the marker's 793000", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the marker is still there after it was used")
	}
}

func TestAMinimumThatWouldNotGoBackIsTriedAgain(t *testing.T) {
	old := retryAfter
	retryAfter = 20 * time.Millisecond
	defer func() { retryAfter = old }()

	root, path := board(t, "793000", "1989000")
	f := &fake{}
	stop := start(t, root, f, 50*time.Millisecond)
	defer stop()

	f.set(true)
	waitFor(t, path, "1989000")
	// The file cannot be written for a while: a directory stands where it is.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	f.set(false)
	time.Sleep(200 * time.Millisecond) // the hold ends and several tries fail
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("1989000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, path, "793000")
}

func TestNoClustersIsNoWork(t *testing.T) {
	if err := run(context.Background(), t.TempDir(), &fake{}, time.Second); err != nil {
		t.Fatal(err)
	}
}

// bigLittle adds a cgroup tree and eight cores, four of them slower, to a root made by board.
func bigLittle(t *testing.T, root string) (cpus string) {
	t.Helper()
	for n := 0; n < 8; n++ {
		d := filepath.Join(root, cpuDir, "cpu"+strconv.Itoa(n))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		c := "741"
		if n >= 4 {
			c = "1024"
		}
		if err := os.WriteFile(filepath.Join(d, "cpu_capacity"), []byte(c+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cg := filepath.Join(root, cgroupDir)
	if err := os.MkdirAll(cg, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"cgroup.controllers", "cgroup.subtree_control", "cgroup.procs"} {
		if err := os.WriteFile(filepath.Join(cg, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(cg, setName, "cpuset.cpus")
}

func TestCoresNarrowedOnlyWhileTouched(t *testing.T) {
	root, path := board(t, "793000", "1989000")
	cpus := bigLittle(t, root)
	f := &fake{}
	stop := start(t, root, f, 300*time.Millisecond)
	defer stop()

	if got := contents(t, cpus); got != "0,1,2,3,4,5,6,7" {
		t.Fatalf("cores %s at start, want all", got)
	}
	f.set(true)
	waitFor(t, cpus, "4,5,6,7")
	waitFor(t, path, "1989000")
	f.set(false)
	time.Sleep(60 * time.Millisecond)
	if got := contents(t, cpus); got != "4,5,6,7" {
		t.Fatalf("cores %s inside the hold, want the fast ones kept", got)
	}
	waitFor(t, cpus, "0,1,2,3,4,5,6,7")
}

func TestTheDaemonIsPutInTheGroupAndLeavesIt(t *testing.T) {
	root, _ := board(t, "793000", "1989000")
	bigLittle(t, root)
	stop := start(t, root, &fake{}, time.Hour)

	procs := filepath.Join(root, cgroupDir, setName, "cgroup.procs")
	if got := contents(t, procs); got != strconv.Itoa(os.Getpid()) {
		t.Fatalf("group members %q, want this process", got)
	}
	stop()
	if got := contents(t, filepath.Join(root, cgroupDir, "cgroup.procs")); got != strconv.Itoa(os.Getpid()) {
		t.Fatalf("root group members %q after stopping, want this process back", got)
	}
}

func TestAGroupLeftNarrowStartsWide(t *testing.T) {
	root, _ := board(t, "793000", "1989000")
	cpus := bigLittle(t, root)
	if err := os.MkdirAll(filepath.Dir(cpus), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cpus, []byte("4,5,6,7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := start(t, root, &fake{}, time.Hour)
	defer stop()
	if got := contents(t, cpus); got != "0,1,2,3,4,5,6,7" {
		t.Fatalf("cores %s, want all after a daemon died narrowed", got)
	}
}

func TestWhatTheDaemonStartedIsMovedOutWhileNarrowed(t *testing.T) {
	root, _ := board(t, "793000", "1989000")
	cpus := bigLittle(t, root)
	// A video player (4242) was born in the group, as everything the daemon starts is; the renderer
	// (777) is the daemon's own program.
	// The decoder is techo5-ffmpeg64 (techo5-ffmpeg on a 32-bit kernel): it starts like the daemon's own
	// names and is not one of them.
	for pid, comm := range map[string]string{"4242": "techo5-ffmpeg64", "777": "techo5-gpu"} {
		d := filepath.Join(root, "proc", pid)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &fake{}
	stop := start(t, root, f, time.Hour)
	defer stop()

	group := filepath.Join(root, cgroupDir, setName, "cgroup.procs")
	free := filepath.Join(root, cgroupDir, freeName, "cgroup.procs")
	if err := os.WriteFile(group, []byte(strconv.Itoa(os.Getpid())+"\n777\n4242\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.set(true)
	waitFor(t, cpus, "4,5,6,7")
	waitFor(t, free, "4242") // moved out, and only that; the renderer and the daemon stay
}

func TestOneKindOfCoreHasNoGroup(t *testing.T) {
	root := t.TempDir()
	for n := 0; n < 4; n++ {
		d := filepath.Join(root, cpuDir, "cpu"+strconv.Itoa(n))
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "cpu_capacity"), []byte("1024\n"), 0o644)
	}
	if c := newCpuset(root); c != nil {
		t.Fatal("a group for cores that are all alike")
	}
}

func TestSwitchedOffTheBoostIsNotRaised(t *testing.T) {
	SetEnabled(false)
	defer SetEnabled(true)
	root, path := board(t, "793000", "1989000")
	cpus := bigLittle(t, root)
	f := &fake{}
	stop := start(t, root, f, 50*time.Millisecond)
	defer stop()

	f.set(true)
	f.set(false)
	time.Sleep(250 * time.Millisecond)
	if got := contents(t, path); got != "793000" {
		t.Fatalf("minimum %s with the boost off, want it left at 793000", got)
	}
	if got := contents(t, cpus); got != "0,1,2,3,4,5,6,7" {
		t.Fatalf("cores %s with the boost off, want all", got)
	}
}

func TestSwitchingOffGivesEverythingBackAtOnce(t *testing.T) {
	defer SetEnabled(true)
	root, path := board(t, "793000", "1989000")
	cpus := bigLittle(t, root)
	f := &fake{}
	stop := start(t, root, f, time.Hour)
	defer stop()

	f.set(true)
	waitFor(t, path, "1989000")
	waitFor(t, cpus, "4,5,6,7")
	SetEnabled(false) // a finger is still down
	waitFor(t, path, "793000")
	waitFor(t, cpus, "0,1,2,3,4,5,6,7")
}

func TestSwitchingOnAgainBoostsTheNextTouch(t *testing.T) {
	SetEnabled(false)
	defer SetEnabled(true)
	root, path := board(t, "793000", "1989000")
	f := &fake{}
	stop := start(t, root, f, 50*time.Millisecond)
	defer stop()

	SetEnabled(true)
	f.set(true)
	waitFor(t, path, "1989000")
	f.set(false)
	waitFor(t, path, "793000")
}

func TestTheRenderersAdoptedWhenItIsNotInTheGroup(t *testing.T) {
	root, _ := board(t, "793000", "1989000")
	cpus := bigLittle(t, root)
	// The renderer (777) is the daemon's child and its own program, but is in no group.
	d := filepath.Join(root, "proc", "777")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	stat := "777 (techo5-gpu) S " + strconv.Itoa(os.Getpid()) + " 1 1 0\n"
	if err := os.WriteFile(filepath.Join(d, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "comm"), []byte("techo5-gpu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	stop := start(t, root, f, time.Hour)
	defer stop()

	group := filepath.Join(root, cgroupDir, setName, "cgroup.procs")
	if err := os.WriteFile(group, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.set(true)
	waitFor(t, cpus, "4,5,6,7")
	if got := contents(t, group); got != "777" {
		t.Fatalf("group members %q after a touch, want the renderer adopted (777)", got)
	}
}

func TestFastCoresOffKeepsTheClocksAndTheCores(t *testing.T) {
	SetFastCores(false)
	defer SetFastCores(true)
	root, path := board(t, "793000", "1989000")
	cpus := bigLittle(t, root)
	f := &fake{}
	stop := start(t, root, f, 300*time.Millisecond)
	defer stop()

	f.set(true)
	waitFor(t, path, "1989000")
	time.Sleep(100 * time.Millisecond)
	if got := contents(t, cpus); got != "0,1,2,3,4,5,6,7" {
		t.Fatalf("cores %s with fast cores off, want all", got)
	}
	f.set(false)
	waitFor(t, path, "793000")
}

func TestSwitchingFastCoresOffGivesTheCoresBackAtOnce(t *testing.T) {
	defer SetFastCores(true)
	root, path := board(t, "793000", "1989000")
	cpus := bigLittle(t, root)
	f := &fake{}
	stop := start(t, root, f, time.Hour)
	defer stop()

	f.set(true)
	waitFor(t, cpus, "4,5,6,7")
	SetFastCores(false) // a finger is still down
	waitFor(t, cpus, "0,1,2,3,4,5,6,7")
	if got := contents(t, path); got != "1989000" {
		t.Fatalf("minimum %s, want the clocks kept while the boost is on", got)
	}
}

func TestAFailedMoveIsLoggedOnceWhileTheMessageStaysTheSame(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	warnMu.Lock()
	lastWarned = ""
	warnMu.Unlock()

	root, _ := board(t, "793000", "1989000")
	bigLittle(t, root)
	c := newCpuset(root)
	if c == nil {
		t.Fatal("no group")
	}
	// Three programs the daemon started sit in the group, and the group they are to be moved to cannot be
	// written: a directory stands where its cgroup.procs is.
	if err := os.WriteFile(filepath.Join(c.dir, "cgroup.procs"), []byte("4242\n4243\n4244\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(c.free, "cgroup.procs")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(c.free, "cgroup.procs"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.sweep()
	c.sweep()
	if got := strings.Count(buf.String(), "cpuboost: a write"); got != 1 {
		t.Fatalf("logged %d times for six failed moves of one kind, want once:\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "cgroup.procs") {
		t.Fatalf("the log does not say which file:\n%s", buf.String())
	}
}
