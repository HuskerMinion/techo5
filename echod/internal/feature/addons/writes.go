package addons

import (
	"bufio"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// How much each add-on writes to the device's storage. The images keep their own writes low, about
// 35 MB a day, so the eMMC lasts (/data is mounted noatime with a 60-second commit, and /tmp and /run
// are memory); an add-on that writes a database every few seconds could wear it many times faster, and
// nothing else would say so. The card shows each running add-on's rate, and one that writes more than
// TECHO5 itself does is said in the log and on the card. Nothing is stopped for it: writing is not
// wrong, only worth knowing.
//
// The measure is the kernel's own, write_bytes in /proc/<pid>/io, less cancelled_write_bytes: what a
// process sent toward the block layer, counted as the page is dirtied, less what was truncated before
// it was written back. A file on tmpfs is never written back, so writes to /tmp, /run and /dev/shm do
// not count, which is right: they are memory. It is read for the add-on's program and every process
// under it, and, for one that runs as a user of its own, every process of that user too, since a
// program can leave its process tree (setsid, a double fork) and its user runs nothing else.
//
// A child that has exited is still counted when its parent in the tree reaped it: the kernel adds a
// reaped child's figures into its parent's. One that was reparented to init and exited between two
// looks takes what it wrote since the last look with it; and the program itself, once it exits, is
// counted to the end from what wait4 says of it (rusage's ru_oublock, the same figure in 512-byte
// blocks). That loss is small next to what the measure is for, a rate over a day.
//
// Everything is kept in memory and starts again at a restart of the daemon: saving it would be the
// very writing it watches for, and an estimate that starts over after a restart is still right within
// the hour.

// writesWarnMB is the rate, in MB a day, past which an add-on is said to write a lot: more than TECHO5's
// own writes, about 35 MB a day, with room to spare so that a busy day of TECHO5's is not the line.
const writesWarnMB = 50

const (
	// writesWindow is the span the rate is taken over once there is that much, and writesFirst the
	// least there has to be before there is a rate at all: under an hour, the burst of a program's
	// first start (a database made, a cache filled) would read as its day.
	writesWindow = 24 * time.Hour
	writesFirst  = time.Hour
)

// writeSample is the running total at one look.
type writeSample struct {
	at    time.Time
	total uint64
}

// writeMeter is one add-on's writes, under the manager's mu.
type writeMeter struct {
	pid  int    // its program while it runs, 0 when it does not
	run  int    // which start of it pid is, so a look begun before a restart is not applied after it
	last uint64 // what its processes had written at the last look of this run
	main uint64 // what its program alone had, of that, with the children it reaped

	total   uint64        // everything counted since the daemon started
	samples []writeSample // the total at each look over the window, and the newest one older than it
	warned  bool          // the log has said it writes a lot, until it no longer does
}

// begin is a start of its program, pid.
func (w *writeMeter) begin(pid int, now time.Time) {
	w.run++
	w.pid, w.last, w.main = pid, 0, 0
	if len(w.samples) == 0 {
		// The span starts with the program, not with the first look ten minutes on.
		w.samples = append(w.samples, writeSample{now, w.total})
	}
}

// add counts what its processes have written by now, cur, of which its program main, for the start
// run of it.
func (w *writeMeter) add(run int, cur, main uint64, now time.Time) {
	if run != w.run || w.pid == 0 {
		return
	}
	// Less than last time is a process that left the tree with what it had written: nothing was
	// unwritten, so nothing is taken off, and the count goes on from the smaller figure.
	if cur > w.last {
		w.total += cur - w.last
	}
	w.last, w.main = cur, main
	w.sample(now)
}

// end is its program's exit, with what wait4 says it and the children it reaped wrote, when known:
// what that has beyond the program's own figure at the last look is what it wrote since. ru_oublock
// does not take cancelled writes off, so this last stretch can count a file truncated before it was
// written back; it is one stretch of ten minutes at most.
func (w *writeMeter) end(final uint64, known bool, now time.Time) {
	if w.pid == 0 {
		return
	}
	if known && final > w.main {
		w.total += final - w.main
	}
	w.pid, w.last, w.main = 0, 0, 0
	w.sample(now)
}

// sample keeps the total at now, and drops what the window no longer needs: every sample but the
// newest one at or before its start.
func (w *writeMeter) sample(now time.Time) {
	w.samples = append(w.samples, writeSample{now, w.total})
	from := now.Add(-writesWindow)
	for len(w.samples) > 2 && !w.samples[1].at.After(from) {
		w.samples = w.samples[1:]
	}
}

// perDay is the rate it writes at, in bytes a day: over the last day once there is a day, and from what
// there is until then, once that is at least an hour. No rate is known before that.
func (w *writeMeter) perDay() (uint64, bool) {
	if len(w.samples) < 2 {
		return 0, false
	}
	first, now := w.samples[0], w.samples[len(w.samples)-1]
	span := now.at.Sub(first.at)
	if span < writesFirst {
		return 0, false
	}
	return uint64(float64(now.total-first.total) * float64(writesWindow) / float64(span)), true
}

// procIO is one process as /proc shows it, for the writes: its parent, its real user, and what it has
// written.
type procIO struct {
	ppid  int
	uid   uint32
	wrote uint64
}

// readProcs is every process running now, by pid, from procDir; a variable, for tests. A process whose
// files cannot be read (it exited during the walk) is left out.
var readProcs = func() map[int]procIO {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	out := make(map[int]procIO, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join(procDir, e.Name())
		p, ok := readStatus(filepath.Join(dir, "status"))
		if !ok {
			continue
		}
		p.wrote = readIO(filepath.Join(dir, "io"))
		out[pid] = p
	}
	return out
}

// readStatus is a process's parent and real user, from its status file.
func readStatus(path string) (procIO, bool) {
	f, err := os.Open(path)
	if err != nil {
		return procIO{}, false
	}
	defer f.Close()
	var p procIO
	have := 0
	s := bufio.NewScanner(f)
	for s.Scan() && have < 2 {
		k, v, ok := strings.Cut(s.Text(), ":")
		if !ok {
			continue
		}
		switch k {
		case "PPid":
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				p.ppid = n
				have++
			}
		case "Uid":
			if fs := strings.Fields(v); len(fs) > 0 {
				if n, err := strconv.ParseUint(fs[0], 10, 32); err == nil {
					p.uid = uint32(n)
					have++
				}
			}
		}
	}
	return p, have == 2
}

// readIO is what a process has written toward storage, from its io file: write_bytes less
// cancelled_write_bytes, and 0 when the file cannot be read (a kernel built without task I/O
// accounting has none, and the card then says nothing rather than something wrong).
func readIO(path string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var wrote, cancelled uint64
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "write_bytes":
			wrote = n
		case "cancelled_write_bytes":
			cancelled = n
		}
	}
	// Cancelled bytes are charged to whoever truncated the file, which need not be who wrote it, so a
	// process can have more of them than it wrote.
	if cancelled > wrote {
		return 0
	}
	return wrote - cancelled
}

// treeWrote is what the processes of one add-on have written: root and everything under it, and with
// byUID, every process of uid as well, each counted once.
func treeWrote(procs map[int]procIO, root int, uid uint32, byUID bool) uint64 {
	children := map[int][]int{}
	for pid, p := range procs {
		children[p.ppid] = append(children[p.ppid], pid)
	}
	seen := map[int]bool{}
	var sum uint64
	var walk func(pid int)
	walk = func(pid int) {
		if seen[pid] {
			return
		}
		p, ok := procs[pid]
		if !ok {
			return
		}
		seen[pid] = true
		sum += p.wrote
		for _, c := range children[pid] {
			walk(c)
		}
	}
	walk(root)
	if byUID {
		for pid, p := range procs {
			if p.uid == uid {
				walk(pid)
			}
		}
	}
	return sum
}

// checkWrites looks at how much each running add-on has written, and says in the log, once each time
// it crosses the line, an add-on that writes more than TECHO5 itself.
func (m *Manager) checkWrites() {
	type look struct {
		a     *addon
		pid   int
		run   int
		uid   uint32
		byUID bool
		id    string
	}
	m.mu.Lock()
	var looks []look
	for _, a := range m.addons {
		if a.writes.pid != 0 {
			looks = append(looks, look{a: a, pid: a.writes.pid, run: a.writes.run, uid: a.rec.UID,
				byUID: !RunsAsRoot(a.man.Root), id: a.rec.ID})
		}
	}
	m.mu.Unlock()
	if len(looks) == 0 {
		return
	}
	procs := readProcs()
	now := m.clock.Now()
	for _, l := range looks {
		wrote := treeWrote(procs, l.pid, l.uid, l.byUID)
		m.mu.Lock()
		l.a.writes.add(l.run, wrote, procs[l.pid].wrote, now)
		rate, known := l.a.writes.perDay()
		much := known && rate > writesWarnMB<<20
		say := much && !l.a.writes.warned
		l.a.writes.warned = much
		m.mu.Unlock()
		if say {
			slog.Warn("addons: an add-on writes more to storage than TECHO5 itself, which wears the device's storage",
				"id", l.id, "mb_a_day", rate>>20)
		}
	}
}
