package fastjpeg

import (
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// widen makes the pipe the picture comes back on a megabyte, the most Linux allows by default, from 64
// KiB: the picture is written all at once, and through a small pipe each 64 KiB is a round of waking
// one process and then the other.
func widen(f *os.File) {
	_, _ = unix.FcntlInt(f.Fd(), unix.F_SETPIPE_SZ, 1<<20)
}

// nobody is the user the helper runs as when the daemon is root: it reads a picture from wherever the
// owner pointed the screen and needs nothing but its pipes.
var nobody = sync.OnceValue(func() *syscall.Credential {
	if os.Getuid() != 0 {
		return nil
	}
	u, err := user.Lookup("nobody")
	if err != nil {
		return nil
	}
	uid, err1 := strconv.ParseUint(u.Uid, 10, 32)
	gid, err2 := strconv.ParseUint(u.Gid, 10, 32)
	if err1 != nil || err2 != nil || uid == 0 || gid == 0 {
		return nil
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}
})

// confine runs the helper as nobody, with nothing of the daemon's environment, and ends it with the daemon.
func confine(cmd *exec.Cmd) {
	cmd.Env = []string{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: nobody(), Pdeathsig: syscall.SIGKILL}
}
