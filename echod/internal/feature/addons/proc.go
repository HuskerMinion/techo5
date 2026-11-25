package addons

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Running an add-on's program: as its own user, in its own process group, with nothing of the daemon's
// environment, unable to gain privileges, bounded in how many processes and how much address space it
// can have, and dying with the daemon.

// cred is who an add-on runs as: nil for root, which only an add-on whose manifest asks for it gets.
type cred struct {
	UID, GID uint32
	Groups   []uint32
}

// spec is one start of an add-on's program.
type spec struct {
	Path string
	Args []string
	Dir  string
	Env  []string
	Cred *cred
	Out  io.Writer // its standard output and error, both

	// Limits, for an add-on that does not run as root: at most NProc processes and threads for its user,
	// and at most AddressMB of address space each (0 for none).
	NProc     int
	AddressMB int
}

// process is a started program.
type process interface {
	// Wait returns once it has exited.
	Wait() error
	// Stop asks it to stop, and kills what is left of it after stopGrace. It returns at once; Wait
	// says when it has gone.
	Stop()
}

// shell is what an unprivileged add-on is started through, to lower its limits before it runs (limited).
var shell = "/bin/sh"

// limited is the command line that starts s's program with its limits already in place: the shell
// (busybox's, as the user it runs as) lowers its own limits, which needs no privilege, and execs the
// program in its place, so the program starts bounded rather than being bounded a moment after it has
// started, the way the video decoder is started (feature/video, wrap). The script is fixed; the program
// and its arguments reach it as its own arguments ("$0" "$@"), never as text it reads as a command.
//
// The limits are set in the child rather than with prlimit after the start, which would leave the
// program running unbounded for as long as that took, and rather than in a shim of the daemon's own
// binary, which would run every package's init as the add-on's user before the exec.
func limited(s spec) (string, []string) {
	if s.Cred == nil || (s.NProc == 0 && s.AddressMB == 0) {
		return s.Path, s.Args
	}
	var script []string
	if s.NProc > 0 {
		script = append(script, "ulimit -u "+strconv.Itoa(s.NProc))
	}
	if s.AddressMB > 0 {
		script = append(script, "ulimit -v "+strconv.Itoa(s.AddressMB*1024))
	}
	script = append(script, `exec "$0" "$@"`)
	return shell, append([]string{"-c", strings.Join(script, " && "), s.Path}, s.Args...)
}

// spawn starts a program; a variable, for tests, which cannot change user.
//
// It is started from a Linux thread of its own, locked for as long as the program runs, for two
// reasons. The kernel's parent-death signal follows the thread that started the child, not the
// process: started from an ordinary goroutine, the add-on would be killed whenever Go retired that
// thread, with the daemon perfectly well (internal/asp, startOnLockedThread, has the same reason). And
// no_new_privs, which keeps the program from gaining privileges through a setuid binary or file
// capabilities, is a flag of the thread that a child inherits, and cannot be cleared once set: so it
// is set on that thread, and the thread is never unlocked, which has Go throw it away when the
// goroutine ends rather than let anything else run on it.
var spawn = func(s spec) (process, error) {
	path, args := limited(s)
	cmd := exec.Command(path, args...)
	cmd.Dir, cmd.Env = s.Dir, s.Env
	cmd.Stdout, cmd.Stderr = s.Out, s.Out
	// A program whose own children hold its output open is not waited on for ever.
	cmd.WaitDelay = stopGrace
	// Its own process group, so a stop reaches whatever it started too; and killed by the kernel if the
	// daemon dies first, so an add-on never outlives the supervisor that answers for it (the next
	// daemon starts it again).
	attr := &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if s.Cred != nil {
		attr.Credential = &syscall.Credential{Uid: s.Cred.UID, Gid: s.Cred.GID, Groups: s.Cred.Groups}
	}
	cmd.SysProcAttr = attr

	p := &osProcess{cmd: cmd, done: make(chan struct{})}
	started := make(chan error)
	go func() {
		runtime.LockOSThread()
		noNewPrivs := s.Cred != nil
		if noNewPrivs {
			if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
				started <- fmt.Errorf("setting no_new_privs: %w", err)
				return // the thread stays locked, and goes with the goroutine
			}
		}
		if err := cmd.Start(); err != nil {
			started <- err
			if !noNewPrivs {
				runtime.UnlockOSThread()
			}
			return
		}
		started <- nil
		p.err = cmd.Wait()
		close(p.done)
		if !noNewPrivs {
			runtime.UnlockOSThread()
		}
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	// The kernel's choice when memory runs out, before anything of the daemon's.
	_ = os.WriteFile("/proc/"+strconv.Itoa(cmd.Process.Pid)+"/oom_score_adj", []byte("900"), 0)
	return p, nil
}

// stopGrace is how long a stopped add-on has to finish before it is killed.
var stopGrace = 5 * time.Second

type osProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

func (p *osProcess) Wait() error { <-p.done; return p.err }

func (p *osProcess) Stop() {
	pgid := -p.cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM)
	go func() {
		select {
		case <-p.done:
		case <-time.After(stopGrace):
			_ = syscall.Kill(pgid, syscall.SIGKILL)
		}
	}()
}

// Limits for an add-on that does not run as root.
//
// nprocLimit bounds its user's processes, and threads, which RLIMIT_NPROC counts as well: a fork bomb
// stops there, and the sweep at stop (killUID) always has an end. A Go program runs a thread per busy
// core and per blocking call, a dozen or two, so the bound leaves it plenty.
//
// The address space is bounded at addressFactor times the memory its listing says it uses, and no
// lower than addressFloorMB (addressFloor64MB for a 64-bit build): RLIMIT_AS counts address space
// reserved and never touched, which a Go runtime takes plenty of, so this is a guard against a program
// that runs away, not its budget. The budget is the memory check before it starts (fits) and the
// kernel's out-of-memory killer, which takes it before the daemon (oom_score_adj).
const (
	nprocLimit       = 128
	addressFactor    = 16
	addressFloorMB   = 1536
	addressFloor64MB = 4096
)

// addressMB is the address-space limit for an add-on that says it uses mb.
func addressMB(mb int, arch string) int {
	floor := addressFloorMB
	if arch == "arm64" {
		floor = addressFloor64MB
	}
	return max(mb*addressFactor, floor)
}

// Who an add-on runs as. Each has a user id of its own, from a range nothing in the images uses. A new
// add-on takes the next id never given out (Root/next-uid), so an id once used is not used again: a
// process left behind by one add-on's user, or a file it owns somewhere, could otherwise be another's.
// Only the same add-on, reinstalled over the state it kept, takes back the id that owns that state. No
// /etc/passwd entry is made or needed: dropping privileges is the kernel's setgroups, setgid and setuid
// in the child before it runs a single instruction of the add-on's (Go's SysProcAttr.Credential), which
// every one of the Linux images has (the Shows', the Spot's and the Dots'), so nothing depends on
// adduser or su being in an image's busybox, or on /etc being writable.
const (
	firstUID = 30000
	lastUID  = 30999
)

// aidInet is the group these Android kernels give a network socket to (Android's AID_INET, whatever
// the image calls it): without it an add-on could not listen on its ports.
const aidInet = 3003

// canDrop is whether this process can run another as a different user, which only root can; a
// variable, for tests.
var canDrop = func() bool { return os.Geteuid() == 0 }

// credFor is the user an add-on with uid runs as, and the network groups it needs.
func credFor(uid uint32) *cred {
	c := &cred{UID: uid, GID: uid, Groups: []uint32{aidInet}}
	if g, err := user.LookupGroup("inet"); err == nil {
		if id, err := strconv.ParseUint(g.Gid, 10, 32); err == nil && uint32(id) != aidInet {
			c.Groups = append(c.Groups, uint32(id))
		}
	}
	return c
}

// errNoUID is an install with every id in the range given out once: a thousand add-ons.
var errNoUID = errors.New("addons: no user id left for another add-on")

// chown hands an add-on's state folder to its user: the folder alone, never what is in it, which that
// user could have made links of to anything root owns. A variable, for tests.
var chown = func(path string, c *cred) {
	if c == nil {
		return
	}
	if err := os.Lchown(path, int(c.UID), int(c.GID)); err != nil {
		slog.Warn("addons: handing the state folder to the add-on's user", "path", path, "err", err)
	}
}

// killUID kills every process of an add-on's user, and goes on until a look finds none. Stopping its
// process group is not enough: a program can start one that leaves the group (setsid), and that one
// would go on running with the add-on's ports closed and its folder deleted, and be there still for
// whatever runs as the user next. A user of the add-on range runs nothing but its add-on, so every one
// of its processes is the add-on's. Its processes are bounded (nprocLimit), so this ends.
func killUID(uid uint32) error {
	for round := 0; round < 100; round++ {
		pids := pidsOf(uid)
		if len(pids) == 0 {
			return nil
		}
		for _, pid := range pids {
			_ = killPID(pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("addons: processes of user %d were still running after it was killed", uid)
}

// killStrays kills every process of the add-on range's users, at the supervisor's start, before it
// starts anything: nothing of theirs should be running then. A daemon that went down without stopping
// its add-ons, or an add-on removed with its state kept whose user left something behind, would
// otherwise leave programs running with no supervisor, no firewall rules and nobody to stop them.
func killStrays() {
	for _, uid := range uidsRunning() {
		if uid >= firstUID && uid <= lastUID {
			slog.Warn("addons: a process of an add-on's user was running before any was started; killed", "uid", uid)
			_ = killUID(uid)
		}
	}
}

// uidsRunning is the real user ids of the processes running now, each once.
func uidsRunning() []uint32 {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	seen := map[uint32]bool{}
	var out []uint32
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procDir, e.Name(), "status"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			rest, ok := strings.CutPrefix(line, "Uid:")
			if !ok {
				continue
			}
			if f := strings.Fields(rest); len(f) > 0 {
				if n, err := strconv.ParseUint(f[0], 10, 32); err == nil && !seen[uint32(n)] {
					seen[uint32(n)] = true
					out = append(out, uint32(n))
				}
			}
			break
		}
	}
	return out
}

// killPID kills one process; a variable, for tests.
var killPID = func(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

// pidsOf is the processes running as uid, by any of their user ids, from /proc.
func pidsOf(uid uint32) []int {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	want := strconv.FormatUint(uint64(uid), 10)
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		f, err := os.Open(filepath.Join(procDir, e.Name(), "status"))
		if err != nil {
			continue
		}
		s := bufio.NewScanner(f)
		for s.Scan() {
			rest, ok := strings.CutPrefix(s.Text(), "Uid:")
			if !ok {
				continue
			}
			for _, id := range strings.Fields(rest) {
				if id == want {
					out = append(out, pid)
					break
				}
			}
			break
		}
		f.Close()
	}
	return out
}

// tmpDirs are the folders anybody can write to, which an add-on's user may have left files in; a
// variable, for tests.
// /run among them: the images mount it 0755 now, but an older initramfs left it writable by anyone.
var tmpDirs = []string{"/tmp", "/var/tmp", "/dev/shm", "/run"}

// sweepUID deletes what an add-on's user left in the shared temporary folders once it has stopped:
// they are memory (tmpfs), and what one add-on leaves there would otherwise stay until a reboot. Only
// entries the user owns, at the top of each folder, and nothing through a link: RemoveAll removes a
// link, not what it points at.
func sweepUID(uid uint32) {
	for _, dir := range tmpDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			st, err := os.Lstat(p)
			if err != nil {
				continue
			}
			if sys, ok := st.Sys().(*syscall.Stat_t); ok && sys.Uid == uid {
				if err := os.RemoveAll(p); err != nil {
					slog.Warn("addons: sweeping what an add-on left", "path", p, "err", err)
				}
			}
		}
	}
}

// duMB is how much a folder holds, in megabytes, not following links; a variable, for tests.
var duMB = func(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n >> 20
}
