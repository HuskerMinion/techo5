package addons

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// What was put on the device by hand: programs started from rc.local, which TECHO5 did not install and
// cannot update or remove. The card lists them with the same warning as an install, without the box.
//
// Two things can be seen, and the card says that is all: the programs rc.local names that live on
// userdata, and the programs running now from userdata that nothing of TECHO5's started. A program
// rc.local starts from the image's own folders, or one that has exited, is not seen; neither is
// anything put somewhere else to start. Only the program's path is shown, never its arguments, which is
// where a key or a token would be.

// RCLocal is the owner's script the image runs last at boot (tools/linux/rootfs/etc/techo5/boot.sh).
var RCLocal = "/data/techo5-linux/rc.local"

// procDir is /proc; a variable, for tests.
var procDir = "/proc"

// Found is a program put on the device by hand.
type Found struct {
	Path    string
	RCLocal bool // named in rc.local
	Running bool // running now
}

// FindHandInstalled is what can be seen of the programs put on the device by hand, by path.
func (m *Manager) FindHandInstalled() []Found {
	byPath := map[string]*Found{}
	get := func(p string) *Found {
		f, ok := byPath[p]
		if !ok {
			f = &Found{Path: p}
			byPath[p] = f
		}
		return f
	}
	for _, p := range rcLocalPrograms(RCLocal) {
		get(p).RCLocal = true
	}
	for _, p := range runningFromData(m.root) {
		get(p).Running = true
	}
	out := make([]Found, 0, len(byPath))
	for _, f := range byPath {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// rcLocalPrograms is the programs on userdata that rc.local runs: the first word of a line, past any
// assignments, nohup or exec, when it is a path under /data. A firewall line, a loop or a sleep is not
// a program of the owner's.
func rcLocalPrograms(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	seen := map[string]bool{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(strings.TrimLeft(line, "( "))
		for len(fields) > 0 {
			w := fields[0]
			if w == "nohup" || w == "exec" || w == "setsid" || (strings.Contains(w, "=") && !strings.HasPrefix(w, "/")) {
				fields = fields[1:]
				continue
			}
			break
		}
		if len(fields) == 0 {
			continue
		}
		p := strings.Trim(fields[0], `"'`)
		if !strings.HasPrefix(p, "/data/") || strings.ContainsAny(p, "$`") {
			continue
		}
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// runningFromData is the programs running now from userdata, other than the daemon, what it started,
// and the add-ons it installed (under root).
func runningFromData(root string) []string {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	self := os.Getpid()
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		exe, err := os.Readlink(filepath.Join(procDir, e.Name(), "exe"))
		if err != nil {
			continue
		}
		exe = strings.TrimSuffix(exe, " (deleted)")
		if !strings.HasPrefix(exe, "/data/") || strings.HasPrefix(exe, root+"/") || seen[exe] {
			continue
		}
		if descendsFrom(pid, self) {
			continue
		}
		seen[exe] = true
		out = append(out, exe)
	}
	return out
}

// descendsFrom is whether pid was started by ancestor, or by something it started.
func descendsFrom(pid, ancestor int) bool {
	for range 32 {
		p := parentOf(pid)
		switch {
		case p == ancestor:
			return true
		case p <= 1:
			return false
		}
		pid = p
	}
	return false
}

// parentOf is the parent's pid, from /proc/<pid>/stat: the fourth field, after the name in brackets,
// which can itself hold spaces and brackets.
func parentOf(pid int) int {
	b, err := os.ReadFile(filepath.Join(procDir, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}
