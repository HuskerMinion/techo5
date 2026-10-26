//go:build linux

// Package gputest is a stand-in for techo5-gpu, for tests of what uses the gpu package: the test binary
// runs itself as the helper, speaks the helper's protocol on fd 3, and writes what it was asked to a
// log the test reads. Only tests import it.
package gputest

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/gpu"
)

const (
	envServe  = "TECHO5_FAKE_GPU"       // set: this process is the helper
	envLog    = "TECHO5_FAKE_GPU_LOG"   // where it writes what it was asked
	envPanel  = "TECHO5_FAKE_GPU_PANEL" // the panel it drives, WxH
	envDie    = "TECHO5_FAKE_GPU_DIE"   // a file: while it exists, the helper exits at its Nth present (its content)
	envRefuse = "TECHO5_FAKE_GPU_REFUSE"
)

// Main runs the fake helper when the test binary was started as one, and does not return then. Call
// it first in a TestMain.
func Main() {
	if os.Getenv(envServe) == "" {
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--reset-color" {
		if f, err := os.OpenFile(os.Getenv(envLog), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%d reset\n", os.Getpid())
			f.Close()
		}
		os.Exit(0)
	}
	serve()
	os.Exit(0)
}

// Config is how the fake helper behaves.
type Config struct {
	Panel string // the panel's size, "WxH"
	// DieAt, when above 0, has the next helper started exit at its DieAt'th present, without
	// replying; the helpers after it do not.
	DieAt int
	// Refuse, when not 0, has every present refused with that errno.
	Refuse syscall.Errno
}

// Log is what the helpers wrote.
type Log struct{ path, die string }

// Use points the gpu package at the test binary as its helper, behaving as cfg says, for the rest of
// the test.
func Use(t testing.TB, cfg Config) *Log {
	t.Helper()
	dir := t.TempDir()
	l := &Log{path: dir + "/log", die: dir + "/die"}
	if cfg.DieAt > 0 {
		if err := os.WriteFile(l.die, []byte(strconv.Itoa(cfg.DieAt)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(envServe, "1")
	t.Setenv(envLog, l.path)
	t.Setenv(envPanel, cfg.Panel)
	t.Setenv(envDie, l.die)
	t.Setenv(envRefuse, strconv.Itoa(int(cfg.Refuse)))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	gpu.SetHelperPath(exe)
	t.Cleanup(func() {
		gpu.Stop(fmt.Errorf("gputest: test over"))
		gpu.SetHelperPath("/nonexistent/techo5-gpu")
	})
	return l
}

// Lines is the log so far, one request a line: "<pid> <op> <fields>".
func (l *Log) Lines() []string {
	b, _ := os.ReadFile(l.path)
	var out []string
	for _, s := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Ops is the lines whose op is op.
func (l *Log) Ops(op string) []string {
	var out []string
	for _, s := range l.Lines() {
		if f := strings.Fields(s); len(f) > 1 && f[1] == op {
			out = append(out, s)
		}
	}
	return out
}

// Gone waits up to a second for the process pid to have ended.
func Gone(pid int) bool {
	for range 100 {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// The helper's side: requests are eight uint32s, replies a status, three uint32s and 112 bytes of text.

type request struct {
	op uint32
	a  [7]uint32
}

type reply struct {
	status int32
	a      [3]uint32
	text   [112]byte
}

func serve() {
	logf, _ := os.OpenFile(os.Getenv(envLog), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	w := bufio.NewWriter(logf)
	say := func(format string, args ...any) {
		fmt.Fprintf(w, "%d "+format+"\n", append([]any{os.Getpid()}, args...)...)
		w.Flush()
	}
	pw, ph := 0, 0
	fmt.Sscanf(os.Getenv(envPanel), "%dx%d", &pw, &ph)
	refuse, _ := strconv.Atoi(os.Getenv(envRefuse))
	dieAt := 0
	if b, err := os.ReadFile(os.Getenv(envDie)); err == nil {
		dieAt, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		os.Remove(os.Getenv(envDie))
	}
	var canvas []byte
	presents := 0
	const sock = 3
	for {
		var req request
		b := unsafe.Slice((*byte)(unsafe.Pointer(&req)), unsafe.Sizeof(req))
		oob := make([]byte, unix.CmsgSpace(3*4))
		n, oobn, _, _, err := unix.Recvmsg(sock, b, oob, 0)
		if err != nil || n != len(b) {
			return
		}
		var fds []int
		if msgs, err := unix.ParseSocketControlMessage(oob[:oobn]); err == nil {
			for _, m := range msgs {
				if got, err := unix.ParseUnixRights(&m); err == nil {
					fds = append(fds, got...)
				}
			}
		}
		var rep reply
		switch req.op {
		case 1: // hello
			copy(rep.text[:], "Fake GPU")
			say("hello")
		case 2: // display open
			if len(fds) > 0 {
				size, _ := unix.Seek(fds[0], 0, 2)
				if mem, err := unix.Mmap(fds[0], 0, int(size), unix.PROT_READ, unix.MAP_SHARED); err == nil {
					canvas = mem
				}
			}
			rep.a[0], rep.a[1] = uint32(pw), uint32(ph)
			say("open %dx%d fd=%v", req.a[0], req.a[1], len(fds) > 0)
		case 3: // present
			presents++
			if dieAt > 0 && presents >= dieAt {
				say("die")
				os.Exit(1)
			}
			if refuse != 0 {
				rep.status = -int32(refuse)
				say("present refused")
				break
			}
			first := "-"
			if len(canvas) >= 4 {
				first = fmt.Sprintf("%d,%d,%d", canvas[0], canvas[1], canvas[2])
			}
			rep.a[0] = 256
			say("present first=%s", first)
		case 4: // present a frame
			say("frame %dx%d layout=%d", req.a[0], req.a[1], req.a[6])
		case 8: // color
			rep.a[1] = 1
			say("color %d", req.a[0])
		case 9: // lift
			say("lift %d", req.a[0])
		default:
			rep.status = -int32(syscall.ENOSYS)
		}
		for _, fd := range fds {
			unix.Close(fd)
		}
		rb := unsafe.Slice((*byte)(unsafe.Pointer(&rep)), unsafe.Sizeof(rep))
		if _, err := unix.Write(sock, rb); err != nil {
			return
		}
	}
}
