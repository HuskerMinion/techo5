//go:build linux

package asp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
)

const (
	// The installation path is a deployment contract, not user configuration.
	startupLimit  = 10 * time.Second
	exchangeLimit = 80 * time.Millisecond
	debtLimit     = 100 * time.Millisecond
)

var components = [...]string{"data", "techo5-linux", "external-asp", "bin", "external-asp"}

type pinnedLaunch struct {
	executable *os.File
	assets     *os.File
}

func (p *pinnedLaunch) close() {
	if p == nil {
		return
	}
	if p.executable != nil {
		_ = p.executable.Close()
	}
	if p.assets != nil {
		_ = p.assets.Close()
	}
}

// openPinned traverses path components using pinned directory FDs and
// O_NOFOLLOW. The /data and /data/techo5-linux ancestors are traversal-only:
// their ownership and permissions vary by device and are not a trust boundary.
// The root-owned, mode 0700 private directories and pinned executable are verified.
// The root/UID arguments are private, enabling trusted-root offline tests only.
func openPinned(root *os.File, owner uint32) (_ *pinnedLaunch, err error) {
	if root == nil {
		return nil, errors.New("nil secure authority root")
	}
	current := root
	var held, assets, executable *os.File
	defer func() {
		if held != nil {
			_ = held.Close()
		}
		if err != nil {
			if assets != nil {
				_ = assets.Close()
			}
			if executable != nil {
				_ = executable.Close()
			}
		}
	}()
	for i, name := range components[:len(components)-1] {
		fd, e := syscall.Openat(int(current.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if e != nil {
			return nil, fmt.Errorf("open %s: %w", name, e)
		}
		dir := os.NewFile(uintptr(fd), name)
		if i >= 2 {
			if e = verifyPinned(dir, owner, i); e != nil {
				_ = dir.Close()
				return nil, fmt.Errorf("verify %s: %w", name, e)
			}
		}
		if held != nil {
			_ = held.Close()
		}
		held, current = dir, dir
		if i == 2 {
			assets = dir
			held = nil
		}
	}
	fd, e := syscall.Openat(int(current.Fd()), components[4], syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, fmt.Errorf("open executable: %w", e)
	}
	executable = os.NewFile(uintptr(fd), "external-asp")
	if e = verifyPinned(executable, owner, 4); e != nil {
		return nil, fmt.Errorf("verify executable: %w", e)
	}
	var magic [4]byte
	if _, e = executable.ReadAt(magic[:], 0); e != nil || magic != [4]byte{0x7f, 'E', 'L', 'F'} {
		return nil, errors.New("executable lacks ELF signature")
	}
	return &pinnedLaunch{executable: executable, assets: assets}, nil
}

func verifyPinned(f *os.File, owner uint32, position int) error {
	var st syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &st); err != nil {
		return err
	}
	if st.Uid != owner {
		return fmt.Errorf("owner UID %d, expected %d", st.Uid, owner)
	}
	mode, perm := st.Mode&syscall.S_IFMT, st.Mode&07777
	if position == 2 || position == 3 {
		if mode != syscall.S_IFDIR {
			return errors.New("not a directory")
		}
		if perm != 0700 {
			return fmt.Errorf("private directory mode %#o", perm)
		}
		return nil
	}
	if mode != syscall.S_IFREG || perm&07000 != 0 || perm&0022 != 0 || perm&0500 != 0500 || st.Size < 4 {
		return fmt.Errorf("unsafe executable mode %#o/size %d", perm, st.Size)
	}
	return nil
}

// Session owns one child and exactly one Wait. Cancel closes IPC immediately;
// Close additionally joins the sole reaper. A session never touches another's
// pipes or process. No global manager, reconnect, status channel or retry.
type Session struct {
	cmd         *exec.Cmd
	in, out     *os.File
	lengths     []uint32
	gate        chan struct{}
	debt        time.Duration // protected by gate; only a new session starts at zero
	stopped     chan struct{}
	done        chan struct{}
	stopOnce    sync.Once
	waitErr     error // published by closing done
	stopContext func() bool
}

func (s *Session) stop() {
	s.stopOnce.Do(func() {
		close(s.stopped)
		if s.in != nil {
			_ = s.in.Close()
		}
		if s.out != nil {
			_ = s.out.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
	})
}

// Cancel is safe during a blocked read/write and does not wait for exchange.
func (s *Session) Cancel() {
	if s != nil {
		s.stop()
	}
}

// Close is idempotent and joins the only reaper; it never calls Wait itself.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	if s.stopContext != nil {
		s.stopContext()
	}
	s.stop()
	<-s.done
	return s.waitErr
}

// Start starts a new secure, PCM-only session and consumes a fresh installation
// snapshot each time. READY alone does not activate External; the first successful PCM does.
func Start(ctx context.Context, descriptor Descriptor) (*Session, error) {
	if ctx == nil {
		return nil, errors.New("nil session context")
	}
	startCtx, cancel := context.WithTimeout(ctx, startupLimit)
	defer cancel()
	wire, err := Encode(descriptor)
	if err != nil {
		return nil, err
	}
	if err = startCtx.Err(); err != nil {
		return nil, err
	}
	root, err := os.Open("/")
	if err != nil {
		return nil, err
	}
	p, err := openPinned(root, 0)
	_ = root.Close()
	if err != nil {
		return nil, fmt.Errorf("secure External ASP: %w", err)
	}
	return startPinned(ctx, startCtx, p, descriptor, wire, []string{"LANG=C", "LC_ALL=C"})
}

// startPinned consumes its verified FDs on all paths. No caller-controlled
// execution pathname: exec uses FD3 and preserves the assets directory as FD4.
func startPinned(ctx, startup context.Context, p *pinnedLaunch, d Descriptor, wire []byte, env []string) (_ *Session, err error) {
	defer p.close()
	if e := startup.Err(); e != nil {
		return nil, e
	}
	if p == nil || p.executable == nil || p.assets == nil {
		return nil, errors.New("missing launch pins")
	}
	// openPinned already validated both inode types, permissions and owner.
	// These exact pinned FDs are passed through ExtraFiles as FD3 and FD4.
	inR, inW, e := os.Pipe()
	if e != nil {
		return nil, e
	}
	outR, outW, e := os.Pipe()
	if e != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, e
	}
	defer inR.Close()
	defer outW.Close()
	cmd := exec.Command("/proc/self/fd/3")
	cmd.ExtraFiles = []*os.File{p.executable, p.assets}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, os.Stderr
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if e := startup.Err(); e != nil {
		_ = inW.Close()
		_ = outR.Close()
		return nil, e
	}
	// Pdeathsig follows the creating Linux thread, not the Go process. Keep
	// that thread locked until the sole reaper has finished with the child.
	done := make(chan struct{})
	if e := startOnLockedThread(cmd, done); e != nil {
		_ = inW.Close()
		_ = outR.Close()
		return nil, fmt.Errorf("start pinned ASP: %w", e)
	}
	// The parent no longer needs the child-side ends or either pinned launch
	// FD. Reaper ownership starts only after a successful cmd.Start.
	_ = inR.Close()
	_ = outW.Close()
	p.close()
	p.executable, p.assets = nil, nil
	s := &Session{
		cmd: cmd, in: inW, out: outR, gate: make(chan struct{}, 1),
		stopped: make(chan struct{}), done: done,
	}
	for _, b := range d.Buffers {
		s.lengths = append(s.lengths, b.Length)
	}
	go func() { s.waitErr = cmd.Wait(); close(s.done) }()
	s.stopContext = context.AfterFunc(ctx, s.stop)
	cancelStartupStop := context.AfterFunc(startup, s.stop)
	defer cancelStartupStop()
	var ready [1]byte
	if e := writeFull(s.in, wire); e != nil {
		s.Close()
		return nil, fmt.Errorf("descriptor: %w", e)
	}
	if _, e := io.ReadFull(s.out, ready[:]); e != nil {
		s.Close()
		return nil, fmt.Errorf("READY read: %w", e)
	}
	if e := startup.Err(); e != nil {
		s.Close()
		return nil, fmt.Errorf("startup: %w", e)
	}
	if ready[0] != ReadyByte {
		s.Close()
		return nil, fmt.Errorf("invalid READY byte %#x", ready[0])
	}
	return s, nil
}

// startOnLockedThread keeps the Linux thread that created the helper alive
// until the one cmd.Wait completes. Without this, Go may retire that thread
// while the daemon is healthy, delivering the kernel's parent-death signal.
func startOnLockedThread(cmd *exec.Cmd, exited <-chan struct{}) error {
	started := make(chan error)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := cmd.Start()
		started <- err
		if err == nil {
			<-exited
		}
	}()
	return <-started
}

func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if n > 0 {
			b = b[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

// Exchange sends borrowed native buffers, then returns a fresh owned 640-byte
// mono S16LE frame. Input is never retained past the call; the returned output
// never aliases future exchanges. One 80ms deadline spans the gate, ALL writes
// and the read, including the first frame after READY.
// Cancellation is terminal for the session, including when blocked on an OS pipe.
func (s *Session) Exchange(ctx context.Context, buffers [][]byte) (_ []byte, retErr error) {
	if s == nil {
		return nil, errors.New("nil session")
	}
	// Every failed exchange is terminal, including caller-side contract errors
	// and cancellation while waiting for the only outstanding PCM operation.
	defer func() {
		if retErr != nil {
			s.stop()
		}
	}()
	if ctx == nil {
		return nil, errors.New("nil exchange context")
	}
	began := time.Now()
	ex, cancel := context.WithDeadline(ctx, began.Add(exchangeLimit))
	defer cancel()
	select {
	case s.gate <- struct{}{}:
		defer func() {
			if retErr != nil {
				s.stop()
			}
			<-s.gate
		}()
	case <-ex.Done():
		return nil, ex.Err()
	case <-s.stopped:
		return nil, errors.New("session closed")
	}
	if err := ex.Err(); err != nil {
		return nil, err
	}
	deadline, _ := ex.Deadline()
	if !time.Now().Before(deadline) {
		return nil, context.DeadlineExceeded
	}
	select {
	case <-s.stopped:
		return nil, errors.New("session closed")
	default:
	}
	if len(buffers) != len(s.lengths) {
		return nil, errors.New("PCM buffer count mismatch")
	}
	for i, b := range buffers {
		if uint64(len(b)) != uint64(s.lengths[i]) {
			return nil, fmt.Errorf("PCM buffer %d length mismatch", i)
		}
	}
	// A timer/cancel closes actual pipes, not merely a detached context.
	cancelExchangeStop := context.AfterFunc(ex, s.stop)
	defer cancelExchangeStop()
	for _, b := range buffers {
		if err := writeFull(s.in, b); err != nil {
			s.stop()
			if ex.Err() != nil {
				return nil, ex.Err()
			}
			return nil, fmt.Errorf("write PCM: %w", err)
		}
	}
	owned := make([]byte, OutputBytes)
	if _, err := io.ReadFull(s.out, owned); err != nil {
		s.stop()
		if ex.Err() != nil {
			return nil, ex.Err()
		}
		return nil, fmt.Errorf("read PCM: %w", err)
	}
	// Disarm before canceling ex on return. A callback that already won makes
	// this result terminal even if it has not closed the pipes yet.
	if err := disarmExchange(ex, cancelExchangeStop); err != nil {
		return nil, err
	}
	select {
	case <-s.stopped:
		return nil, errors.New("session closed")
	default:
	}
	finished := time.Now()
	// A delayed context timer must not admit PCM past the absolute deadline.
	if !finished.Before(deadline) {
		return nil, context.DeadlineExceeded
	}
	next, err := exchangeDebt(s.debt, finished.Sub(began))
	if err != nil {
		return nil, err
	}
	s.debt = next
	return owned, nil
}

// exchangeDebt accounts only completed exchanges, including time at the gate.
// Fast replies repay excess processing time without an idle reset.
func exchangeDebt(debt, elapsed time.Duration) (time.Duration, error) {
	if elapsed >= exchangeLimit {
		return debt, context.DeadlineExceeded
	}
	next := max(0, debt+elapsed-20*time.Millisecond)
	if next >= debtLimit {
		return next, errors.New("External ASP processing debt exceeded")
	}
	return next, nil
}

func disarmExchange(ctx context.Context, stop func() bool) error {
	if !stop() {
		return errors.New("exchange cancellation started")
	}
	return ctx.Err()
}
