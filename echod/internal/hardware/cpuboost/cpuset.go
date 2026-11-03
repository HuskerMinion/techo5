//go:build linux && !dot && !spot

package cpuboost

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	cpuDir    = "/sys/devices/system/cpu"
	cgroupDir = "/sys/fs/cgroup"
	setName   = "techo5-ui"
	// freeName is the group beside it that keeps every core: what the daemon starts that is not the
	// interface is moved there.
	freeName = "techo5-ui-free"
	// The names of the daemon's own programs, which stay in the narrowed group: the daemon and its
	// renderer. Whole names: the video decoder is techo5-ffmpeg64, and is not the interface.
	ownDaemon   = "techo5"
	ownRenderer = "techo5-gpu"
)

// cpuset is the group the daemon's interface lives in: every core most of the time, the fast ones only
// while a finger is on the screen. It is nil where the kernel has no cpusets or the board has one kind of
// core, and all its methods then do nothing.
type cpuset struct {
	root      string
	dir, free string // the narrowed group's directory, and the one that keeps every core
	rootProcs string // cgroup.procs of the root group, where the members go when the daemon stops
	all, fast string // cpu lists
	self      int
	narrowed  bool
}

// newCpuset makes the groups, mounting cgroup2 first where nothing has, and puts the daemon and its
// renderer in the first. What the daemon starts later is born in it too, and is moved out (sweep).
func newCpuset(root string) *cpuset {
	fast, all := cores(root)
	if fast == "" {
		return nil
	}
	cg := filepath.Join(root, cgroupDir)
	if _, err := os.Stat(filepath.Join(cg, "cgroup.controllers")); err != nil {
		_ = os.MkdirAll(cg, 0o755)
		// As systemd mounts it: nothing on it is run, no device node on it is used.
		if err := syscall.Mount("none", cg, "cgroup2", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, ""); err != nil {
			slog.Warn("cpuboost: no cgroups, so no fast-core preference", "err", err)
			return nil
		}
	}
	if b, _ := os.ReadFile(filepath.Join(cg, "cgroup.subtree_control")); !strings.Contains(string(b), "cpuset") {
		if err := os.WriteFile(filepath.Join(cg, "cgroup.subtree_control"), []byte("+cpuset"), 0o644); err != nil {
			slog.Warn("cpuboost: no cpuset controller, so no fast-core preference", "err", err)
			return nil
		}
	}
	c := &cpuset{
		root: root, dir: filepath.Join(cg, setName), free: filepath.Join(cg, freeName),
		rootProcs: filepath.Join(cg, "cgroup.procs"), all: all, fast: fast, self: os.Getpid(),
	}
	for _, d := range []string{c.dir, c.free} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			slog.Warn("cpuboost: no cpuset group", "err", err)
			return nil
		}
		// A daemon that died while narrowed left the group narrow: it starts wide.
		if err := os.WriteFile(filepath.Join(d, "cpuset.cpus"), []byte(all), 0o644); err != nil {
			slog.Warn("cpuboost: the cpuset would not take its cores", "err", err)
			return nil
		}
	}
	for _, pid := range append([]int{c.self}, c.ownChildren()...) {
		move(filepath.Join(c.dir, "cgroup.procs"), strconv.Itoa(pid))
	}
	return c
}

// move puts the process in the group whose cgroup.procs is path. One that has already ended is not a
// failure; anything else is logged, as every other write here is (warn).
func move(path string, pid string) {
	if err := os.WriteFile(path, []byte(pid), 0o644); err != nil && !errors.Is(err, syscall.ESRCH) {
		warn(path, err)
	}
}

// ours is whether pid is the daemon or one of its own programs (its renderer): the interface.
func (c *cpuset) ours(pid int) bool {
	if pid == c.self {
		return true
	}
	b, err := os.ReadFile(filepath.Join(c.root, "proc", strconv.Itoa(pid), "comm"))
	name := strings.TrimSpace(string(b))
	return err == nil && (name == ownDaemon || name == ownRenderer)
}

// ownChildren are the daemon's own programs that are its children now.
func (c *cpuset) ownChildren() []int {
	var out []int
	for _, pid := range children(c.root, c.self) {
		if c.ours(pid) {
			out = append(out, pid)
		}
	}
	return out
}

// sweep moves what is in the narrowed group and is not the interface to the group that keeps every core:
// a video player or AirPlay is decoded and played on all of them, touch or no touch.
func (c *cpuset) sweep() {
	if c == nil {
		return
	}
	b, _ := os.ReadFile(filepath.Join(c.dir, "cgroup.procs"))
	for _, f := range strings.Fields(string(b)) {
		pid, err := strconv.Atoi(f)
		if err != nil || c.ours(pid) {
			continue
		}
		move(filepath.Join(c.free, "cgroup.procs"), f)
	}
}

// adopt puts the daemon's own programs that are not in the group into it: the renderer may have been
// started where the group did not hold it yet, and it is the interface's too.
func (c *cpuset) adopt() {
	b, _ := os.ReadFile(filepath.Join(c.dir, "cgroup.procs"))
	in := map[int]bool{}
	for _, f := range strings.Fields(string(b)) {
		if pid, err := strconv.Atoi(f); err == nil {
			in[pid] = true
		}
	}
	for _, pid := range c.ownChildren() {
		if !in[pid] {
			move(filepath.Join(c.dir, "cgroup.procs"), strconv.Itoa(pid))
		}
	}
}

// narrow limits the group to the fast cores, after moving out what is not the interface and in what is.
func (c *cpuset) narrow() {
	if c == nil {
		return
	}
	if !c.narrowed {
		c.adopt()
	}
	c.sweep()
	if c.narrowed {
		return
	}
	if write(filepath.Join(c.dir, "cpuset.cpus"), c.fast) == nil {
		c.narrowed = true
	}
}

// widen gives the group every core back.
func (c *cpuset) widen() {
	if c == nil || !c.narrowed {
		return
	}
	if write(filepath.Join(c.dir, "cpuset.cpus"), c.all) == nil {
		c.narrowed = false
	}
}

// close widens the group and sends the members of both back to the root group, so that what outlives
// the daemon is limited by nothing.
func (c *cpuset) close() {
	if c == nil {
		return
	}
	_ = write(filepath.Join(c.dir, "cpuset.cpus"), c.all)
	for _, d := range []string{c.dir, c.free} {
		b, _ := os.ReadFile(filepath.Join(d, "cgroup.procs"))
		for _, pid := range strings.Fields(string(b)) {
			move(c.rootProcs, pid)
		}
	}
}

// cores reads each core's capacity: the fast list is the cores with the most of it, and all every core.
// Both are empty where every core is the same, as on a board with one kind.
func cores(root string) (fast, all string) {
	files, _ := filepath.Glob(filepath.Join(root, cpuDir, "cpu[0-9]*", "cpu_capacity"))
	capacity := map[int]int{}
	top := 0
	for _, f := range files {
		n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(filepath.Dir(f)), "cpu"))
		if err != nil {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		c, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			continue
		}
		capacity[n] = c
		top = max(top, c)
	}
	var every, quick []int
	for n, c := range capacity {
		every = append(every, n)
		if c == top {
			quick = append(quick, n)
		}
	}
	if len(quick) == 0 || len(quick) == len(every) {
		return "", ""
	}
	return list(quick), list(every)
}

func list(ns []int) string {
	sort.Ints(ns)
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}

// children are the processes whose parent is pid, from /proc: the renderer is started before the boost.
func children(root string, pid int) []int {
	dirs, _ := filepath.Glob(filepath.Join(root, "proc", "[0-9]*", "stat"))
	var out []int
	for _, f := range dirs {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...: the command may hold spaces and brackets, so after the last ')'.
		s := string(b)
		i := strings.LastIndex(s, ")")
		if i < 0 {
			continue
		}
		fs := strings.Fields(s[i+1:])
		if len(fs) < 2 {
			continue
		}
		if pp, _ := strconv.Atoi(fs[1]); pp == pid {
			n, _ := strconv.Atoi(filepath.Base(filepath.Dir(f)))
			out = append(out, n)
		}
	}
	return out
}
