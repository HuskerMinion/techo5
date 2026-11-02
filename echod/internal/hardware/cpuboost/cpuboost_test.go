//go:build linux && !dot && !spot

package cpuboost

import (
	"context"
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
	mu   sync.Mutex
	down bool
	fns  []func(bool)
}

func (f *fake) Touched() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.down
}

func (f *fake) Listen(fn func(bool)) func() {
	f.mu.Lock()
	f.fns = append(f.fns, fn)
	f.mu.Unlock()
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

// board is a root with one cluster whose minimum is cur, lowest the hardware's, top its maximum.
func board(t *testing.T, cur, lowest, top string) (root, minPath string) {
	t.Helper()
	root = t.TempDir()
	d := filepath.Join(root, cpufreqDir, "policy4")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"scaling_min_freq": cur, "cpuinfo_min_freq": lowest, "cpuinfo_max_freq": top} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, filepath.Join(d, "scaling_min_freq")
}

func minimum(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func waitFor(t *testing.T, path, want string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if minimum(t, path) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("minimum is %s, want %s", minimum(t, path), want)
}

func start(t *testing.T, root string, f *fake, hold time.Duration) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = run(ctx, root, f, hold); close(done) }()
	time.Sleep(20 * time.Millisecond) // the listener is in before the first touch
	return func() { cancel(); <-done }
}

func TestRaisedWhileTouchedAndAfterTheHold(t *testing.T) {
	root, path := board(t, "793000", "793000", "1989000")
	f := &fake{}
	stop := start(t, root, f, 80*time.Millisecond)
	defer stop()

	f.set(true)
	waitFor(t, path, "1989000")
	f.set(false)
	time.Sleep(30 * time.Millisecond)
	if got := minimum(t, path); got != "1989000" {
		t.Fatalf("minimum %s inside the hold, want it kept up", got)
	}
	waitFor(t, path, "793000")
}

func TestATouchInsideTheHoldKeepsItUp(t *testing.T) {
	root, path := board(t, "793000", "793000", "1989000")
	f := &fake{}
	stop := start(t, root, f, 80*time.Millisecond)
	defer stop()

	f.set(true)
	f.set(false)
	time.Sleep(50 * time.Millisecond)
	f.set(true)
	time.Sleep(100 * time.Millisecond)
	if got := minimum(t, path); got != "1989000" {
		t.Fatalf("minimum %s while a finger is down, want it up", got)
	}
	f.set(false)
	waitFor(t, path, "793000")
}

func TestPutBackWhenTheDaemonStops(t *testing.T) {
	root, path := board(t, "793000", "793000", "1989000")
	f := &fake{}
	stop := start(t, root, f, time.Hour)

	f.set(true)
	waitFor(t, path, "1989000")
	stop()
	if got := minimum(t, path); got != "793000" {
		t.Fatalf("minimum %s after stopping, want 793000", got)
	}
}

func TestAMinimumLeftAtTheMaximumGoesBackToTheLowest(t *testing.T) {
	// A daemon that died while boosted left the minimum at the maximum; the next one must not take
	// that for the board's own setting.
	root, path := board(t, "1989000", "500000", "1989000")
	f := &fake{}
	stop := start(t, root, f, 20*time.Millisecond)
	defer stop()

	if got := minimum(t, path); got != "500000" {
		t.Fatalf("minimum %s at start, want the hardware's lowest, 500000", got)
	}
	f.set(true)
	waitFor(t, path, "1989000")
	f.set(false)
	waitFor(t, path, "500000")
}

func TestATapBetweenTwoLooksStillBoosts(t *testing.T) {
	root, path := board(t, "793000", "793000", "1989000")
	f := &fake{}
	stop := start(t, root, f, 200*time.Millisecond)
	defer stop()

	f.set(true)
	f.set(false) // lifted before the loop looked
	waitFor(t, path, "1989000")
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
		d := filepath.Join(root, cpuDir, "cpu"+string(rune('0'+n)))
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
	root, path := board(t, "793000", "793000", "1989000")
	cpus := bigLittle(t, root)
	f := &fake{}
	stop := start(t, root, f, 80*time.Millisecond)
	defer stop()

	if got := minimum(t, cpus); got != "0,1,2,3,4,5,6,7" {
		t.Fatalf("cores %s at start, want all", got)
	}
	f.set(true)
	waitFor(t, cpus, "4,5,6,7")
	waitFor(t, path, "1989000")
	f.set(false)
	time.Sleep(30 * time.Millisecond)
	if got := minimum(t, cpus); got != "4,5,6,7" {
		t.Fatalf("cores %s inside the hold, want the fast ones kept", got)
	}
	waitFor(t, cpus, "0,1,2,3,4,5,6,7")
}

func TestTheDaemonIsPutInTheGroupAndLeavesIt(t *testing.T) {
	root, _ := board(t, "793000", "793000", "1989000")
	bigLittle(t, root)
	f := &fake{}
	stop := start(t, root, f, time.Hour)

	procs := filepath.Join(root, cgroupDir, setName, "cgroup.procs")
	if got := minimum(t, procs); got != strconv.Itoa(os.Getpid()) {
		t.Fatalf("group members %q, want this process", got)
	}
	stop()
	if got := minimum(t, filepath.Join(root, cgroupDir, "cgroup.procs")); got != strconv.Itoa(os.Getpid()) {
		t.Fatalf("root group members %q after stopping, want this process back", got)
	}
}

func TestAGroupLeftNarrowStartsWide(t *testing.T) {
	root, _ := board(t, "793000", "793000", "1989000")
	cpus := bigLittle(t, root)
	if err := os.MkdirAll(filepath.Dir(cpus), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cpus, []byte("4,5,6,7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := start(t, root, &fake{}, time.Hour)
	defer stop()
	if got := minimum(t, cpus); got != "0,1,2,3,4,5,6,7" {
		t.Fatalf("cores %s, want all after a daemon died narrowed", got)
	}
}

func TestOneKindOfCoreHasNoGroup(t *testing.T) {
	root := t.TempDir()
	for n := 0; n < 4; n++ {
		d := filepath.Join(root, cpuDir, "cpu"+string(rune('0'+n)))
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "cpu_capacity"), []byte("1024\n"), 0o644)
	}
	if c := newCpuset(root); c != nil {
		t.Fatal("a group for cores that are all alike")
	}
}
