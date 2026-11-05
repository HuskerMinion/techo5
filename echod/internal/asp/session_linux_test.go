//go:build linux

package asp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const mapText = "ASP1-CHANNEL-MAP-V1\nmic=0,1,2,3\nref=0,1\n"

func supported() Descriptor {
	d := Descriptor{Buffers: []Buffer{
		{Offset: 0, Length: 30720, SampleRate: 96000, Frames: 1920, Stride: 16, Format: FormatS32, ValidBits: 24, Channels: 4},
		{Offset: 30720, Length: 7680, SampleRate: 96000, Frames: 1920, Stride: 4, Format: FormatS16, ValidBits: 16, Channels: 2, FirstChannel: 4},
	}}
	for i := 0; i < 4; i++ {
		d.Channels = append(d.Channels, Channel{BufferIndex: 0, Role: RoleMIC, RoleIndex: uint16(i), FrameOffset: uint32(4 * i)})
	}
	for i := 0; i < 2; i++ {
		d.Channels = append(d.Channels, Channel{BufferIndex: 1, Role: RoleREF, RoleIndex: uint16(i), FrameOffset: uint32(2 * i)})
	}
	return d
}
func pcm() [][]byte {
	m, r := make([]byte, 30720), make([]byte, 7680)
	for i := 0; i < 1920; i++ {
		for j, v := range []uint32{1024, 2048, 4096, 8192} {
			binary.LittleEndian.PutUint32(m[i*16+j*4:], v)
		}
		binary.LittleEndian.PutUint16(r[i*4:], 111)
		binary.LittleEndian.PutUint16(r[i*4+2:], uint16(65536-222))
	}
	return [][]byte{m, r}
}
func copyExecutable(t *testing.T, source, dest string) {
	t.Helper()
	f, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	o, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0500)
	if e != nil {
		t.Fatal(e)
	}
	_, e = io.Copy(o, f)
	if c := o.Close(); e == nil {
		e = c
	}
	if e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T, executable string) (*os.File, string, []string) {
	t.Helper()
	base := t.TempDir()
	if e := os.Chmod(base, 0755); e != nil {
		t.Fatal(e)
	}
	for _, pair := range []struct {
		path string
		mode os.FileMode
	}{
		{"data", 0755}, {"data/techo5-linux", 0755}, {"data/techo5-linux/external-asp", 0700},
		{"data/techo5-linux/external-asp/bin", 0700},
	} {
		if e := os.Mkdir(filepath.Join(base, pair.path), pair.mode); e != nil {
			t.Fatal(e)
		}
	}
	assets := filepath.Join(base, "data/techo5-linux/external-asp")
	if e := os.WriteFile(filepath.Join(assets, "map"), []byte(mapText), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(assets, "vendor"), []byte("test sentinel"), 0600); e != nil {
		t.Fatal(e)
	}
	copyExecutable(t, executable, filepath.Join(assets, "bin/external-asp"))
	root, e := os.Open(base)
	if e != nil {
		t.Fatal(e)
	}
	env := []string{"LANG=C", "LC_ALL=C", "ASP_TEST_CHILD=1", "ASP1_TEST_MAP=" + filepath.Join(assets, "map"), "ASP1_TEST_RESOURCE=" + filepath.Join(assets, "vendor")}
	return root, assets, env
}
func startFixture(t *testing.T, ctx context.Context, root *os.File, d Descriptor, env []string) (*Session, error) {
	t.Helper()
	start, cancel := context.WithTimeout(ctx, startupLimit)
	defer cancel()
	wire, e := Encode(d)
	if e != nil {
		return nil, e
	}
	p, e := openPinned(root, uint32(os.Getuid()))
	if e != nil {
		return nil, e
	}
	return startPinned(ctx, start, p, d, wire, env)
}

// A standard Go test launches the same production IPC path, with FD3 holding
// the verified executable and FD4 the pinned assets directory.
func TestGoChildIPCAndFDPins(t *testing.T) {
	root, assets, env := fixture(t, selfExecutable(t))
	defer root.Close()
	s, err := startFixture(t, context.Background(), root, supported(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	executable, err := os.Stat(filepath.Join(assets, "bin/external-asp"))
	if err != nil {
		t.Fatal(err)
	}
	fd3, err := os.Stat("/proc/" + itoa(s.cmd.Process.Pid) + "/fd/3")
	if err != nil || !os.SameFile(executable, fd3) {
		t.Fatalf("FD3 pin: %v", err)
	}
	directory, err := os.Stat(assets)
	if err != nil {
		t.Fatal(err)
	}
	fd4, err := os.Stat("/proc/" + itoa(s.cmd.Process.Pid) + "/fd/4")
	if err != nil || !os.SameFile(directory, fd4) {
		t.Fatalf("FD4 pin: %v", err)
	}
	first, err := s.Exchange(context.Background(), pcm())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != OutputBytes || !bytes.Equal(first[:128], make([]byte, 128)) || bytes.Equal(first[128:], make([]byte, 512)) {
		t.Fatal("unexpected first simulated output frame")
	}
	previous := bytes.Clone(first)
	second, err := s.Exchange(context.Background(), pcm())
	if err != nil || len(second) != OutputBytes || !bytes.Equal(first, previous) {
		t.Fatalf("output ownership or exchange failed: %v", err)
	}
	s.Cancel()
	_ = s.Close()
	select {
	case <-s.done:
	default:
		t.Fatal("single reaper did not finish")
	}
}

func TestOneOutstandingExchange(t *testing.T) {
	root, _, env := fixture(t, selfExecutable(t))
	defer root.Close()
	s, err := startFixture(t, context.Background(), root, supported(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const concurrent = 12
	errs := make(chan error, concurrent)
	var wg sync.WaitGroup
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, e := s.Exchange(context.Background(), pcm())
			if e == nil && len(out) != OutputBytes {
				e = errors.New("non-fixed output")
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("serialized concurrent caller exchange: %v", e)
		}
	}
}

// Ancestry above external-asp is traversed securely, but not trusted:
// Spot may have /data owned by a device user and mode 0771, while other
// platforms expose a root-owned, mode 0777 /data.
func TestSecureParentDirectoriesAreTraversalOnly(t *testing.T) {
	root, assets, _ := fixture(t, selfExecutable(t))
	defer root.Close()
	base := filepath.Dir(filepath.Dir(filepath.Dir(assets)))
	for _, dir := range []struct {
		path string
		mode os.FileMode
	}{
		{base, 0777},
		{filepath.Join(base, "data"), 0771},
		{filepath.Join(base, "data/techo5-linux"), 0777},
	} {
		if err := os.Chmod(dir.path, dir.mode); err != nil {
			t.Fatal(err)
		}
	}
	owner := uint32(os.Getuid())
	p, err := openPinned(root, owner)
	if err != nil {
		t.Fatalf("rejected traversal-only parent directories: %v", err)
	}
	p.close()
	if err := os.Chmod(assets, 0755); err != nil {
		t.Fatal(err)
	}
	if p, err := openPinned(root, owner); err == nil {
		p.close()
		t.Fatal("unsafe external-asp directory accepted")
	}
	if err := os.Chmod(assets, 0700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(base, "data")
	if err := os.Rename(data, data+"-real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(data+"-real", data); err != nil {
		t.Fatal(err)
	}
	if p, err := openPinned(root, owner); err == nil {
		p.close()
		t.Fatal("symlinked ancestor accepted")
	}
}

func TestSecurePinnedPathsAndReplacement(t *testing.T) {
	executable := selfExecutable(t)
	root, assets, _ := fixture(t, executable)
	defer root.Close()
	owner := uint32(os.Getuid())
	p, e := openPinned(root, owner)
	if e != nil {
		t.Fatal(e)
	}
	old, e := p.executable.Stat()
	if e != nil {
		t.Fatal(e)
	}
	dst := filepath.Join(assets, "bin/external-asp")
	replacement := dst + "-new"
	copyExecutable(t, executable, replacement)
	if e := os.Rename(replacement, dst); e != nil {
		t.Fatal(e)
	}
	fresh, e := openPinned(root, owner)
	if e != nil {
		t.Fatal(e)
	}
	now, e := fresh.executable.Stat()
	if e != nil {
		t.Fatal(e)
	}
	if os.SameFile(old, now) {
		t.Fatal("new activation reused old pinned executable")
	}
	if stable, e := p.executable.Stat(); e != nil || !os.SameFile(old, stable) {
		t.Fatal("old session pin was replaced")
	}
	p.close()
	fresh.close()
	if _, e := openPinned(root, owner+1); e == nil {
		t.Fatal("wrong owner accepted")
	}
	if e := os.Chmod(dst, 0777); e != nil {
		t.Fatal(e)
	}
	if _, e := openPinned(root, owner); e == nil {
		t.Fatal("writable executable accepted")
	}
	if e := os.Chmod(dst, 0500); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(dst, dst+"-real"); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(dst+"-real", dst); e != nil {
		t.Fatal(e)
	}
	if _, e := openPinned(root, owner); e == nil {
		t.Fatal("executable symlink accepted")
	}
	if e := os.Remove(dst); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(dst+"-real", dst); e != nil {
		t.Fatal(e)
	}
	bin := filepath.Join(assets, "bin")
	if e := os.Rename(bin, bin+"-real"); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(bin+"-real", bin); e != nil {
		t.Fatal(e)
	}
	if _, e := openPinned(root, owner); e == nil {
		t.Fatal("directory symlink accepted")
	}
	if e := os.Remove(bin); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(bin+"-real", bin); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(bin, 0777); e != nil {
		t.Fatal(e)
	}
	if _, e := openPinned(root, owner); e == nil {
		t.Fatal("writable private directory accepted")
	}
	if e := os.Chmod(bin, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(dst); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(dst, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := openPinned(root, owner); e == nil {
		t.Fatal("executable directory accepted")
	}
}

func stubSession(t *testing.T, program, behavior string, d Descriptor, ctx context.Context) (*Session, error) {
	t.Helper()
	root, _, env := fixture(t, program)
	defer root.Close()
	return startFixture(t, ctx, root, d, append(env, "ASP_TEST_BEHAVIOR="+behavior))
}
func TestProcessFailureAndCancellation(t *testing.T) {
	program := selfExecutable(t)
	for _, kind := range []string{"bad-ready", "short", "exit"} {
		t.Run(kind, func(t *testing.T) {
			s, e := stubSession(t, program, kind, supported(), context.Background())
			if kind == "bad-ready" {
				if e == nil {
					s.Close()
					t.Fatal("wrong READY accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			_, e = s.Exchange(context.Background(), pcm())
			if e == nil {
				t.Fatal("short/EOF response accepted")
			}
			s.Close()
			s.Close()
			select {
			case <-s.done:
			default:
				t.Fatal("Wait not reaped")
			}
		})
	}
	t.Run("read-cancel", func(t *testing.T) {
		s, e := stubSession(t, program, "sleep", supported(), context.Background())
		if e != nil {
			t.Fatal(e)
		}
		result := make(chan error, 1)
		go func() { _, e := s.Exchange(context.Background(), pcm()); result <- e }()
		time.Sleep(10 * time.Millisecond)
		then := time.Now()
		s.Cancel()
		if time.Since(then) > time.Second {
			t.Fatal("Cancel blocked on exchange")
		}
		select {
		case e := <-result:
			if e == nil {
				t.Fatal("canceled read succeeded")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("pipe read not released")
		}
		s.Close()
	})
	t.Run("old-session-isolation", func(t *testing.T) {
		s1, e := stubSession(t, program, "sleep", supported(), context.Background())
		if e != nil {
			t.Fatal(e)
		}
		s2, e := stubSession(t, program, "sleep", supported(), context.Background())
		if e != nil {
			t.Fatal(e)
		}
		s1.Cancel()
		s1.Close()
		select {
		case <-s2.stopped:
			t.Fatal("old cleanup killed newer session")
		default:
		}
		s2.Close()
	})
}

func TestGlobalExchangeTimeout(t *testing.T) {
	program := selfExecutable(t)
	t.Run("blocked-read", func(t *testing.T) {
		// A two-byte input fits in the pipe even though this child never
		// reads it, so the blocked operation is the output read.
		d := Descriptor{Buffers: []Buffer{{Length: 2, SampleRate: 16000, Frames: 1, Stride: 2, Format: FormatS16, ValidBits: 16, Channels: 1}}, Channels: []Channel{{Role: RoleMIC}}}
		s, e := stubSession(t, program, "sleep", d, context.Background())
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		start := time.Now()
		out, err := s.Exchange(context.Background(), [][]byte{{0, 0}})
		e = err
		dt := time.Since(start)
		if out != nil {
			t.Fatal("expired first PCM returned")
		}
		if !errors.Is(e, context.DeadlineExceeded) || dt < exchangeLimit || dt > exchangeLimit+time.Second {
			t.Fatalf("read shared deadline: %v after %s", e, dt)
		}
		assertExchangeStopped(t, s)
	})
	t.Run("blocked-write", func(t *testing.T) {
		d := Descriptor{Buffers: []Buffer{{Length: 8 * 1024 * 1024, SampleRate: 16000, Frames: 1, Stride: 2, Format: FormatS16, ValidBits: 16, Channels: 1}}, Channels: []Channel{{Role: RoleMIC}}}
		s, e := stubSession(t, program, "sleep", d, context.Background())
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		start := time.Now()
		_, e = s.Exchange(context.Background(), [][]byte{make([]byte, d.Buffers[0].Length)})
		dt := time.Since(start)
		if !errors.Is(e, context.DeadlineExceeded) || dt < exchangeLimit || dt > exchangeLimit+time.Second {
			t.Fatalf("write shared deadline: %v after %s", e, dt)
		}
		assertExchangeStopped(t, s)
	})
}

func TestPartialPCMTimeout(t *testing.T) {
	s, _, out := exchangePipes(t)
	// Queue only half a frame before Exchange so the full read must wait for
	// more bytes. No scheduler-sensitive Sleep is needed to arrange the partial reply.
	if _, err := out.Write(make([]byte, OutputBytes/2)); err != nil {
		t.Fatal(err)
	}
	then := time.Now()
	pcm, err := s.Exchange(context.Background(), [][]byte{{1}})
	elapsed := time.Since(then)
	if pcm != nil {
		t.Fatal("incomplete PCM was accepted")
	}
	if !errors.Is(err, context.DeadlineExceeded) || elapsed < exchangeLimit || elapsed > exchangeLimit+time.Second {
		t.Fatalf("partial PCM timeout: %v after %s", err, elapsed)
	}
	assertExchangeStopped(t, s)
}

// A rename/symlink race can yield either the originally trusted inode or an
// error, never a different target reached by following the attacker's link.
func TestSecureConcurrentRenameRace(t *testing.T) {
	root, assets, _ := fixture(t, selfExecutable(t))
	defer root.Close()
	real := filepath.Join(assets, "bin/external-asp")
	expected, e := os.Stat(real)
	if e != nil {
		t.Fatal(e)
	}
	bin := filepath.Join(assets, "bin")
	hidden := bin + "-hidden"
	failures := make(chan error, 1)
	go func() {
		for i := 0; i < 150; i++ {
			if e := os.Rename(bin, hidden); e != nil {
				failures <- e
				return
			}
			if e := os.Symlink(hidden, bin); e != nil {
				failures <- e
				return
			}
			runtime.Gosched()
			if e := os.Remove(bin); e != nil {
				failures <- e
				return
			}
			if e := os.Rename(hidden, bin); e != nil {
				failures <- e
				return
			}
		}
		failures <- nil
	}()
	for i := 0; i < 1000; i++ {
		p, e := openPinned(root, uint32(os.Getuid()))
		if e == nil {
			actual, statErr := p.executable.Stat()
			p.close()
			if statErr != nil || !os.SameFile(expected, actual) {
				t.Fatalf("rename raced into an untrusted executable: %v", statErr)
			}
		}
	}
	if e := <-failures; e != nil {
		t.Fatal(e)
	}
}

func TestAssetsPinAndReplacementBetweenActivations(t *testing.T) {
	root, assets, _ := fixture(t, selfExecutable(t))
	defer root.Close()
	owner := uint32(os.Getuid())
	old, err := openPinned(root, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer old.close()
	oldStat, err := old.assets.Stat()
	if err != nil {
		t.Fatal(err)
	}
	moved := assets + "-prior"
	if err := os.Rename(assets, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(assets, 0700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(assets, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, selfExecutable(t), filepath.Join(bin, "external-asp"))
	fresh, err := openPinned(root, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.close()
	newStat, err := fresh.assets.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(oldStat, newStat) {
		t.Fatal("assets directory reused across new activation")
	}
	stillOld, err := old.assets.Stat()
	if err != nil || !os.SameFile(oldStat, stillOld) {
		t.Fatalf("prior assets pin changed: %v", err)
	}
}

func TestExecutableReinstalledBetweenSessions(t *testing.T) {
	root, assets, env := fixture(t, selfExecutable(t))
	defer root.Close()
	s1, e := startFixture(t, context.Background(), root, supported(), env)
	if e != nil {
		t.Fatal(e)
	}
	defer s1.Close()
	dst := filepath.Join(assets, "bin/external-asp")
	replacement := dst + "-update"
	copyExecutable(t, selfExecutable(t), replacement)
	if e := os.Rename(replacement, dst); e != nil {
		t.Fatal(e)
	}
	s2, e := startFixture(t, context.Background(), root, supported(), env)
	if e != nil {
		t.Fatal(e)
	}
	defer s2.Close()
	if same, _ := os.Stat("/proc/" + itoa(s1.cmd.Process.Pid) + "/fd/3"); same == nil {
		t.Fatal("old executable FD pin lost")
	}
	if got, e := s1.Exchange(context.Background(), pcm()); e != nil || len(got) != OutputBytes {
		t.Fatalf("old session after installation replacement: %v", e)
	}
	if got, e := s2.Exchange(context.Background(), pcm()); e != nil || len(got) != OutputBytes {
		t.Fatalf("new session after installation replacement: %v", e)
	}
}
func TestStartFailureAndNoFDLeaks(t *testing.T) {
	before, e := os.ReadDir("/proc/self/fd")
	if e != nil {
		t.Fatal(e)
	}
	invalid := filepath.Join(t.TempDir(), "invalid-ELF")
	if e := os.WriteFile(invalid, []byte{0x7f, 'E', 'L', 'F'}, 0500); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 10; i++ {
		root, _, env := fixture(t, invalid)
		_, e := startFixture(t, context.Background(), root, supported(), env)
		root.Close()
		if e == nil || !strings.Contains(e.Error(), "start pinned ASP") {
			t.Fatalf("invalid ELF must fail Start (attempt %d): %v", i, e)
		}
	}
	for i := 0; i < 10; i++ {
		root, _, env := fixture(t, selfExecutable(t))
		s, e := startFixture(t, context.Background(), root, supported(), env)
		root.Close()
		if e != nil {
			t.Fatal(e)
		}
		s.Cancel()
		s.Close()
		s.Close()
		select {
		case <-s.done:
		default:
			t.Fatal("Wait not joined")
		}
	}
	runtime.GC()
	after, e := os.ReadDir("/proc/self/fd")
	if e != nil {
		t.Fatal(e)
	}
	if len(after) > len(before)+2 {
		t.Fatalf("FD leak after success/Start failures: before=%d after=%d", len(before), len(after))
	}
}

func TestNoIdleTimerAndStartupSurvival(t *testing.T) {
	if testing.Short() {
		t.Skip("real >10 second startup-deadline survival")
	}
	root, _, env := fixture(t, selfExecutable(t))
	defer root.Close()
	s, e := startFixture(t, context.Background(), root, supported(), env)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	before := time.Now()
	time.Sleep(10*time.Second + 200*time.Millisecond)
	o, e := s.Exchange(context.Background(), pcm())
	if e != nil || len(o) != 640 || time.Since(before) <= 10*time.Second {
		t.Fatalf("READY child died after startup timer: %v", e)
	}
	// Also proves >5s idle pause does not terminate session.
}
func TestStartupTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("real 10 second startup deadline")
	}
	p := selfExecutable(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	_, e := stubSession(t, p, "start-wait", supported(), ctx)
	dt := time.Since(start)
	if e == nil || dt < startupLimit-400*time.Millisecond || dt > startupLimit+2*time.Second {
		t.Fatalf("startup deadline not enforced: %v (%s)", e, dt)
	}
}
func TestStartupCancelAndNoZombie(t *testing.T) {
	p := selfExecutable(t)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); time.Sleep(150 * time.Millisecond); cancel() }()
	start := time.Now()
	_, e := stubSession(t, p, "start-wait", supported(), ctx)
	wg.Wait()
	if e == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("cancel startup did not release pipes: %v", e)
	}
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }
func TestNoProgress(t *testing.T) {
	if !errors.Is(writeFull(zeroWriter{}, []byte{1, 2}), io.ErrNoProgress) {
		t.Fatal("zero-byte progress loop")
	}
}
func TestBorrowedInputSizeAndOldOutput(t *testing.T) {
	runtime.GC()
	root, _, env := fixture(t, selfExecutable(t))
	defer root.Close()
	for _, malformed := range [][][]byte{
		{make([]byte, 30720)},
		{make([]byte, 30720), make([]byte, 7679)},
	} {
		s, e := startFixture(t, context.Background(), root, supported(), env)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Exchange(context.Background(), malformed); e == nil {
			t.Fatal("malformed input accepted")
		}
		select {
		case <-s.stopped:
		default:
			t.Fatal("invalid PCM input did not terminate session")
		}
		_ = s.Close()
	}
	s, e := startFixture(t, context.Background(), root, supported(), env)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	o, e := s.Exchange(context.Background(), pcm())
	if e != nil {
		t.Fatal(e)
	}
	old := bytes.Clone(o)
	_, e = s.Exchange(context.Background(), pcm())
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(o, old) {
		t.Fatal("owned output reused")
	}
}
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var a [20]byte
	n := len(a)
	for i > 0 {
		n--
		a[n] = byte('0' + i%10)
		i /= 10
	}
	return string(a[n:])
}

func TestExchangeDebt(t *testing.T) {
	ms := time.Millisecond
	repeat := func(n int, d time.Duration) []time.Duration {
		out := make([]time.Duration, n)
		for i := range out {
			out[i] = d
		}
		return out
	}
	for _, tc := range []struct {
		name    string
		initial time.Duration
		elapsed []time.Duration
		want    time.Duration
		failAt  int
	}{
		{"zero floor", 0, []time.Duration{ms}, 0, 0},
		{"gradual repayment", 50 * ms, []time.Duration{10 * ms, 10 * ms}, 30 * ms, 0},
		{"repayment floor", ms, []time.Duration{10 * ms}, 0, 0},
		{"below debt boundary", debtLimit - time.Nanosecond, []time.Duration{20 * ms}, debtLimit - time.Nanosecond, 0},
		{"at debt boundary", debtLimit - time.Nanosecond, []time.Duration{20*ms + time.Nanosecond}, debtLimit, 1},
		{"five 40ms", 0, repeat(5, 40*ms), debtLimit, 5},
		{"ten 30ms", 0, repeat(10, 30*ms), debtLimit, 10},
		{"five 25ms", 0, repeat(5, 25*ms), 25 * ms, 0},
		{"historical pair", 0, []time.Duration{77478 * time.Microsecond, 40056 * time.Microsecond}, 77534 * time.Microsecond, 0},
		{"historical pair repaid", 0, append([]time.Duration{77478 * time.Microsecond, 40056 * time.Microsecond}, repeat(8, 10*ms)...), 0, 0},
		{"delayed candidate with debt", 42522 * time.Microsecond, []time.Duration{77478 * time.Microsecond}, debtLimit, 1},
		{"below timeout boundary", 0, []time.Duration{exchangeLimit - time.Nanosecond}, 60*ms - time.Nanosecond, 0},
		{"at timeout boundary", 0, []time.Duration{exchangeLimit}, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			debt := tc.initial
			failed := 0
			for i, elapsed := range tc.elapsed {
				var err error
				debt, err = exchangeDebt(debt, elapsed)
				if err != nil {
					failed = i + 1
					if elapsed >= exchangeLimit && !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("wrong timeout error: %v", err)
					}
					break
				}
			}
			if failed != tc.failAt || debt != tc.want {
				t.Fatalf("debt=%s failure=%d, want %s/%d", debt, failed, tc.want, tc.failAt)
			}
		})
	}
}

// These are real OS pipes with a test-controlled peer, without a child process.
// Tests of kill and the sole reaper use startFixture instead.
func exchangePipes(t *testing.T) (*Session, *os.File, *os.File) {
	t.Helper()
	in, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, out, err := os.Pipe()
	if err != nil {
		in.Close()
		inW.Close()
		t.Fatal(err)
	}
	s := &Session{in: inW, out: outR, lengths: []uint32{1}, gate: make(chan struct{}, 1), stopped: make(chan struct{}), done: make(chan struct{})}
	close(s.done) // There is no process to reap in this fixture.
	t.Cleanup(func() { s.Close(); in.Close(); out.Close() })
	return s, in, out
}

func assertExchangeStopped(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.stopped:
	default:
		t.Fatal("failed exchange did not stop the session")
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not join the reaper")
	}
	if s.cmd != nil && s.cmd.ProcessState == nil {
		t.Fatal("child was not reaped")
	}
}

// An expired parent whose timer has not run yet: Err/Done deliberately lag
// behind the fixed absolute deadline, as during a scheduler stall.
type laggingDeadline struct {
	context.Context
	deadline time.Time
}

func (c laggingDeadline) Deadline() (time.Time, bool) { return c.deadline, true }

func TestExchangeGateDeadline(t *testing.T) {
	for _, mode := range []string{"waiting", "canceled", "expired without timer"} {
		t.Run(mode, func(t *testing.T) {
			s, in, _ := exchangePipes(t)
			ctx := context.Background()
			switch mode {
			case "waiting":
				s.gate <- struct{}{}
				defer func() { <-s.gate }()
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c // Both the gate and Done are ready.
			case "expired without timer":
				ctx = laggingDeadline{ctx, time.Now().Add(-time.Second)}
			}
			out, err := s.Exchange(ctx, [][]byte{{1}})
			if err == nil || out != nil {
				t.Fatal("gate admitted canceled/expired PCM")
			}
			if mode != "canceled" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			var input [1]byte
			if n, err := in.Read(input[:]); n != 0 || err != io.EOF {
				t.Fatalf("IPC touched before gate validation: %d/%v", n, err)
			}
			assertExchangeStopped(t, s) // Cleanup must not need the gate.
		})
	}
}

func TestExchangeCompletionBoundary(t *testing.T) {
	for _, mode := range []string{"cancel wins", "deadline without timer", "debt rejects PCM"} {
		t.Run(mode, func(t *testing.T) {
			s, in, out := exchangePipes(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var exchangeCtx context.Context = ctx
			deadline := time.Now().Add(exchangeLimit / 2)
			if mode == "deadline without timer" {
				exchangeCtx = laggingDeadline{context.Background(), deadline}
			}
			if mode == "debt rejects PCM" {
				s.debt = debtLimit - time.Millisecond
			}
			type reply struct {
				pcm []byte
				err error
			}
			result := make(chan reply, 1)
			go func() { b, err := s.Exchange(exchangeCtx, [][]byte{{1}}); result <- reply{b, err} }()
			var input [1]byte
			if _, err := io.ReadFull(in, input[:]); err != nil {
				t.Fatal(err)
			}
			if mode == "cancel wins" {
				cancel()
				select {
				case <-s.stopped:
				case <-time.After(time.Second):
					t.Fatal("cancel callback did not stop pipes")
				}
			} else {
				if mode == "deadline without timer" {
					time.Sleep(time.Until(deadline))
				}
				if mode == "debt rejects PCM" {
					// Start below the budget; the completed reply must cross it.
					time.Sleep(30 * time.Millisecond)
				}
				if _, err := out.Write(make([]byte, OutputBytes)); err != nil {
					t.Fatal(err)
				}
			}
			var r reply
			select {
			case r = <-result:
			case <-time.After(time.Second):
				t.Fatal("completion blocked")
			}
			if r.err == nil || r.pcm != nil {
				t.Fatal("rejected PCM returned")
			}
			if mode == "deadline without timer" && !errors.Is(r.err, context.DeadlineExceeded) {
				t.Fatal(r.err)
			}
			if mode == "debt rejects PCM" && !strings.Contains(r.err.Error(), "processing debt") {
				t.Fatalf("completed reply was not rejected by debt: %v", r.err)
			}
			assertExchangeStopped(t, s)
		})
	}
}

func TestNewSessionDebtAndCompletedCallCancellation(t *testing.T) {
	root, _, env := fixture(t, selfExecutable(t))
	defer root.Close()
	s, err := startFixture(t, context.Background(), root, supported(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.debt != 0 {
		t.Fatal("new session inherited debt")
	}
	const initialDebt = 50 * time.Millisecond
	s.debt = initialDebt
	ctx, cancel := context.WithCancel(context.Background())
	started := time.Now()
	if _, err := s.Exchange(ctx, pcm()); err != nil {
		cancel()
		t.Fatal(err)
	}
	outerElapsed := time.Since(started)
	cancel()
	// The internal Exchange duration lies within the measured outer interval.
	// Checking both bounds rejects a missing debt update on a fast exchange.
	minDebt := initialDebt - 20*time.Millisecond
	maxDebt := minDebt + outerElapsed
	if s.debt < minDebt || s.debt > maxDebt {
		t.Fatalf("debt=%s after Exchange, want [%s, %s]", s.debt, minDebt, maxDebt)
	}
	if _, err := s.Exchange(context.Background(), pcm()); err != nil {
		t.Fatalf("completed call's cancel stopped live session: %v", err)
	}
}

// Exercise the cancellation/disarm race without depending on timer scheduling.
// The started callback stays blocked, proving that disarm does not join it.
func TestExchangeAfterFuncDisarm(t *testing.T) {
	for _, callbackWins := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		stop := context.AfterFunc(ctx, func() {
			close(started)
			<-release
			close(done)
		})
		if callbackWins {
			cancel()
			<-started
		}
		err := disarmExchange(ctx, stop)
		if (err != nil) != callbackWins {
			t.Fatalf("callbackWins=%v, disarm error=%v", callbackWins, err)
		}
		cancel()
		close(release)
		if callbackWins {
			<-done
		} else {
			select {
			case <-started:
				t.Fatal("disarmed callback ran after cancellation")
			default:
			}
		}
	}
}

func TestExchangeCancelAfterDisarm(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { close(called) })
	err := disarmExchange(ctx, func() bool {
		stopped := stop()
		cancel() // Cancellation wins after disarm, before the Err check.
		return stopped
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation after disarm accepted: %v", err)
	}
	select {
	case <-called:
		t.Fatal("disarmed callback ran")
	default:
	}
}
