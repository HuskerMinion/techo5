package streaming

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Restarting: soon after a program that ran a while stops, then backing off to a minute for one that
// keeps stopping at once (a bad stream makes shairport-sync give up, for one).
var (
	restartFirst = 2 * time.Second
	restartMost  = time.Minute
	ranLong      = 30 * time.Second
)

// program is one of the programs this runs: what to run, where its standard output goes (nil for
// nowhere), what is added to the daemon's environment for it, and who it runs as (nil for root).
// What it logs goes to the daemon's log, a line at a time, under name, and only the lines keep says
// are worth keeping (nil keeps every one).
type program struct {
	name   string
	path   string
	args   []string
	stdout io.Writer
	env    []string
	cred   *syscall.Credential
	keep   func(line string) bool

	// started, when set, is told each time the program is about to start, the first time and again.
	started func()
}

// supervise runs p until ctx ends, starting it again whenever it stops.
func supervise(ctx context.Context, p program) { superviseTries(ctx, p, 0) }

// superviseTries is supervise, but gives up once p has stopped soon after starting (within ranLong) tries
// times in a row, and says whether it did; with tries 0 it never does.
func superviseTries(ctx context.Context, p program, tries int) (gaveUp bool) {
	wait := restartFirst
	quick := 0
	for ctx.Err() == nil {
		if p.started != nil {
			p.started()
		}
		start := time.Now()
		err := runProgram(ctx, p)
		if ctx.Err() != nil {
			return false
		}
		slog.Warn("streaming: a program stopped", "program", p.name, "after", time.Since(start).Round(time.Second), "err", err)
		if time.Since(start) > ranLong {
			wait, quick = restartFirst, 0
		} else {
			wait, quick = min(wait*2, restartMost), quick+1
		}
		if tries > 0 && quick >= tries {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
	}
	return false
}

// runProgram runs p once, until it stops or ctx ends. Asked to stop, it gets a moment to say goodbye
// on the network before it is killed.
func runProgram(ctx context.Context, p program) error {
	cmd := exec.CommandContext(ctx, p.path, p.args...)
	cmd.Stdout = p.stdout
	if len(p.env) > 0 {
		cmd.Env = append(os.Environ(), p.env...)
	}
	if p.cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: p.cred}
	}
	cmd.Stderr = &logLines{name: p.name, keep: p.keep}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second
	return cmd.Run()
}

// receiverUser is who the receivers run as: they take in what any phone on the network sends, and
// need no more than a socket (the kernel gives one only to the inet group) and their pipes.
const receiverUser = "streaming"

// receiverCred is the receivers' user, or nil, with a warning, on an image that has not got one: then
// they run as root, as they did before the image made it.
func receiverCred() *syscall.Credential {
	u, err := user.Lookup(receiverUser)
	if err != nil {
		slog.Warn("streaming: no user for the receivers in this image; they run as root", "user", receiverUser, "err", err)
		return nil
	}
	uid, err1 := strconv.ParseUint(u.Uid, 10, 32)
	gid, err2 := strconv.ParseUint(u.Gid, 10, 32)
	if err1 != nil || err2 != nil || uid == 0 {
		slog.Warn("streaming: the receivers' user is not one to run as; they run as root", "user", receiverUser)
		return nil
	}
	c := &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	if g, err := user.LookupGroup("inet"); err == nil {
		if id, err := strconv.ParseUint(g.Gid, 10, 32); err == nil {
			c.Groups = []uint32{uint32(id)}
		}
	}
	if c.Groups == nil {
		slog.Warn("streaming: no inet group in this image; the receivers may be refused a network socket")
	}
	return c
}

// handTo makes path the receivers' user's, where they run as one: path alone, never what is under it,
// which that user could have made links of to anything root owns.
func handTo(cred *syscall.Credential, path string) {
	if cred == nil {
		return
	}
	_ = os.Lchown(path, int(cred.Uid), int(cred.Gid))
}

// logLines is a program's standard error, in the daemon's log a line at a time, the last ones kept
// short: these programs say a lot, and only what goes wrong is worth the space.
type logLines struct {
	name string
	keep func(string) bool
	part []byte
}

func (l *logLines) Write(p []byte) (int, error) {
	l.part = append(l.part, p...)
	for {
		i := indexByte(l.part, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(l.part[:i]), "\r")
		l.part = l.part[i+1:]
		if line != "" && (l.keep == nil || l.keep(line)) {
			slog.Debug("streaming: program says", "program", l.name, "line", clip(line, 300))
		}
	}
	if len(l.part) > 4096 {
		l.part = l.part[:0]
	}
	return len(p), nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
