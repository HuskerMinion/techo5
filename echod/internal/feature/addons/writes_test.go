package addons

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const mb = 1 << 20

// What /proc says is read the way the kernel writes it: the parent and the real user from status,
// and write_bytes less cancelled_write_bytes from io.
func TestReadProcs(t *testing.T) {
	old := procDir
	t.Cleanup(func() { procDir = old })
	procDir = t.TempDir()
	put := func(pid, status, io string) {
		dir := filepath.Join(procDir, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644); err != nil {
			t.Fatal(err)
		}
		if io != "" {
			if err := os.WriteFile(filepath.Join(dir, "io"), []byte(io), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	put("1000", "Name:\tplex\nState:\tS (sleeping)\nPPid:\t1\nUid:\t30000\t30000\t30000\t30000\n",
		"rchar: 99\nwchar: 5000000\nread_bytes: 0\nwrite_bytes: 3000000\ncancelled_write_bytes: 1000000\n")
	put("1001", "Name:\tchild\nPPid:\t1000\nUid:\t30000\t30000\t30000\t30000\n",
		"write_bytes: 10\ncancelled_write_bytes: 4096\n") // charged with another's truncation
	put("1002", "Name:\tnoio\nPPid:\t1\nUid:\t0\t0\t0\t0\n", "")               // a kernel without I/O accounting
	put("1003", "Name:\tbroken\n", "write_bytes: 5\n")                         // went in the middle of the walk
	if err := os.MkdirAll(filepath.Join(procDir, "self"), 0o755); err != nil { // not a process
		t.Fatal(err)
	}
	got := readProcs()
	want := map[int]procIO{1000: {1, 30000, 2000000}, 1001: {1000, 30000, 0}, 1002: {1, 0, 0}}
	if len(got) != len(want) {
		t.Fatalf("read %v, want %v", got, want)
	}
	for pid, p := range want {
		if got[pid] != p {
			t.Errorf("pid %d: %+v, want %+v", pid, got[pid], p)
		}
	}
}

// An add-on's writes are its program's and everything under it, and with a user of its own, whatever
// else that user runs; nothing of anybody else's.
func TestTreeWrote(t *testing.T) {
	procs := map[int]procIO{
		1:    {0, 0, 7 * mb},     // init
		500:  {1, 0, 9 * mb},     // the daemon
		1000: {500, 30000, 1},    // the add-on
		1001: {1000, 30000, 10},  // its child
		1002: {1001, 0, 100},     // a grandchild, setuid away (as root runs one)
		1003: {1, 30000, 1000},   // one that left the tree, still its user's
		1004: {1, 30001, 10000},  // another add-on's
		1005: {1004, 30000, 1e5}, // under another's, as this one's user: counted by the user
	}
	if got := treeWrote(procs, 1000, 30000, true); got != 1+10+100+1000+1e5 {
		t.Errorf("as a user of its own: %d", got)
	}
	if got := treeWrote(procs, 1000, 0, false); got != 1+10+100 {
		t.Errorf("as root: %d", got)
	}
	if got := treeWrote(procs, 4242, 0, false); got != 0 {
		t.Errorf("gone: %d", got)
	}
}

// The rate is unknown for the first hour, from what there is until a day, and over the last day after.
func TestTheRateOverADay(t *testing.T) {
	var w writeMeter
	at := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	w.begin(1000, at)
	look := func(after time.Duration, cur uint64) {
		at = at.Add(after)
		w.add(w.run, cur, cur, at)
	}
	look(10*time.Minute, 1*mb)
	if _, ok := w.perDay(); ok {
		t.Error("a rate after ten minutes")
	}
	for i := 0; i < 5; i++ {
		look(10*time.Minute, uint64(i+2)*mb)
	}
	// 6 MB in an hour is 144 MB a day.
	if r, ok := w.perDay(); !ok || r != 144*mb {
		t.Errorf("after an hour: %d MB, %v", r/mb, ok)
	}
	// Two days on at 1 MB an hour, the first hour's burst is out of the window.
	cur := uint64(6 * mb)
	for i := 0; i < 48*6; i++ {
		cur += mb / 6
		look(10*time.Minute, cur)
	}
	if r, ok := w.perDay(); !ok || r < 23*mb || r > 25*mb {
		t.Errorf("after two days: %d MB, %v", r/mb, ok)
	}
	if len(w.samples) > 24*6+2 {
		t.Errorf("kept %d samples", len(w.samples))
	}
}

// A process that left with what it wrote takes nothing off; a start again counts from nothing; a look
// begun before a restart is not applied after it; and the exit counts the last of it.
func TestTheTotalOnlyGrows(t *testing.T) {
	var w writeMeter
	at := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	w.begin(1000, at)
	w.add(w.run, 10*mb, 2*mb, at.Add(time.Minute))
	w.add(w.run, 4*mb, 3*mb, at.Add(2*time.Minute)) // a child reparented to init went
	w.add(w.run, 6*mb, 5*mb, at.Add(3*time.Minute))
	if w.total != 12*mb {
		t.Errorf("total %d MB, want 12", w.total/mb)
	}
	w.end(8*mb, true, at.Add(4*time.Minute)) // 3 MB more of the program's own since the last look
	if w.total != 15*mb {
		t.Errorf("after the exit: %d MB, want 15", w.total/mb)
	}
	stale := w.run
	w.begin(1001, at.Add(5*time.Minute))
	w.add(stale, 100*mb, 100*mb, at.Add(6*time.Minute))
	w.add(w.run, 1*mb, 1*mb, at.Add(7*time.Minute))
	w.end(0, false, at.Add(8*time.Minute))
	w.add(w.run, 50*mb, 50*mb, at.Add(9*time.Minute)) // after the exit, before a start
	if w.total != 16*mb {
		t.Errorf("after a restart: %d MB, want 16", w.total/mb)
	}
}

// syncBuffer is a log that the supervisor's goroutines can write to while a test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A running add-on's rate is on its status; one that writes more than TECHO5 is said in the log once
// each time it crosses the line; and its exit counts what wait4 says of it.
func TestARunningAddonsWrites(t *testing.T) {
	r, _ := plexRig(t)
	var log syncBuffer
	oldLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLog) })

	if err := r.m.Install(context.Background(), "techo5-plex", "0.1.0", r.shown("techo5-plex", "0.1.0"), true); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateRunning)
	r.m.mu.Lock()
	uid := r.m.addons["techo5-plex"].rec.UID
	r.m.mu.Unlock()
	r.procs.mu.Lock()
	pid := r.procs.procs[0].pid
	r.procs.mu.Unlock()

	var mu sync.Mutex
	var own, escaped uint64
	readProcs = func() map[int]procIO {
		mu.Lock()
		defer mu.Unlock()
		return map[int]procIO{
			pid:     {1, uid, own},
			pid + 1: {1, uid, escaped},
			pid + 2: {1, 0, 1 << 40}, // the daemon's own, never counted
		}
	}
	look := func(ownMB, escapedMB uint64) {
		mu.Lock()
		own, escaped = ownMB*mb, escapedMB*mb
		mu.Unlock()
		r.clock.Advance(10 * time.Minute)
		r.m.checkWrites()
	}
	for i := uint64(1); i <= 6; i++ {
		look(i, 0)
	}
	// 6 MB over the hour since it started: 144 MB a day, past the line.
	s := r.state("techo5-plex")
	if !s.WritesKnown || s.WritesPerDay != 144*mb || !s.WritesMuch {
		t.Errorf("after an hour: %+v", s)
	}
	look(7, 0)
	if n := strings.Count(log.String(), "writes more to storage"); n != 1 {
		t.Errorf("said %d times, want once:\n%s", n, log.String())
	}

	// Writing nothing for a day brings it under the line, and writing again says it again.
	for i := 0; i < 24*6; i++ {
		look(7, 0)
	}
	if s := r.state("techo5-plex"); !s.WritesKnown || s.WritesPerDay != 0 || s.WritesMuch {
		t.Errorf("after a quiet day: %+v", s)
	}
	look(7, 100)
	if n := strings.Count(log.String(), "writes more to storage"); n != 2 {
		t.Errorf("said %d times, want twice", n)
	}

	// The exit counts what wait4 says beyond the last look.
	r.m.mu.Lock()
	before := r.m.addons["techo5-plex"].writes.total
	r.m.mu.Unlock()
	r.procs.mu.Lock()
	p := r.procs.procs[0]
	r.procs.mu.Unlock()
	final := uint64(17 * mb)
	p.mu.Lock()
	p.wrote = &final
	p.mu.Unlock()
	if err := r.m.Stop("techo5-plex"); err != nil {
		t.Fatal(err)
	}
	r.waitState("techo5-plex", StateStopped)
	r.m.mu.Lock()
	after := r.m.addons["techo5-plex"].writes.total
	r.m.mu.Unlock()
	if after-before != 10*mb {
		t.Errorf("the exit added %d MB, want 10", (after-before)/mb)
	}
}
