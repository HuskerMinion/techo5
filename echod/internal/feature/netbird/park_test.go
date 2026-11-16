//go:build !dot

package netbird

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

const keyA = "AAAAAAAA-1111-2222-3333-444444444444"
const keyB = "BBBBBBBB-1111-2222-3333-444444444444"

// parkRig fakes the client: whether it answers status, what cli is given, and a log to read.
type parkRig struct {
	f      *Feature
	up     atomic.Bool
	mu     sync.Mutex
	keys   []string
	cliErr error
	logs   bytes.Buffer
}

func (r *parkRig) logged() string { r.mu.Lock(); defer r.mu.Unlock(); return r.logs.String() }

type lockedWriter struct{ r *parkRig }

func (w lockedWriter) Write(b []byte) (int, error) {
	w.r.mu.Lock()
	defer w.r.mu.Unlock()
	return w.r.logs.Write(b)
}

func newRig(t *testing.T) *parkRig {
	r := &parkRig{}
	wasStatus, wasCli, wasEnc, wasAvail := status, cli, encrypted, Available
	wasDir, wasPoll, wasTO, wasJoin := stateDir, parkPoll, parkTimeout, joinTimeout
	wasLog := slog.Default()
	t.Cleanup(func() {
		status, cli, encrypted, Available = wasStatus, wasCli, wasEnc, wasAvail
		stateDir, parkPoll, parkTimeout, joinTimeout = wasDir, wasPoll, wasTO, wasJoin
		slog.SetDefault(wasLog)
	})
	dir := t.TempDir()
	config.Use(filepath.Join(dir, "config.json"))
	stateDir = dir
	parkPoll, parkTimeout, joinTimeout = 5*time.Millisecond, 2*time.Second, 2*time.Second
	encrypted = func() bool { return true }
	Available = func() bool { return true }
	slog.SetDefault(slog.New(slog.NewTextHandler(lockedWriter{r}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	status = func(context.Context) (daemonStatus, error) {
		if !r.up.Load() {
			return daemonStatus{}, errors.New("not running")
		}
		r.mu.Lock()
		joined := len(r.keys) > 0 && r.cliErr == nil
		r.mu.Unlock()
		if joined {
			return daemonStatus{DaemonStatus: connected, IP: "100.64.0.9"}, nil
		}
		return daemonStatus{DaemonStatus: needsLogin}, nil
	}
	cli = func(_ context.Context, env []string, _ ...string) ([]byte, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		said := "failed"
		for _, e := range env {
			if k, ok := strings.CutPrefix(e, "NB_SETUP_KEY="); ok {
				r.keys = append(r.keys, k)
				said = "failed with " + k // the client may echo the key; it must be scrubbed
			}
		}
		return []byte(said), r.cliErr
	}
	r.f = &Feature{sw: Get().sw, status: Get().status, reload: make(chan struct{}, 1)}
	return r
}

func (r *parkRig) downloading() {
	r.f.set(func(s *Status) { s.Downloading, s.Got = true, 0.3 })
}

func (r *parkRig) finish() {
	r.f.set(func(s *Status) { s.Downloading, s.Got = false, 1 })
	r.up.Store(true)
}

func (r *parkRig) wait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func (r *parkRig) idle() bool {
	r.f.pk.Lock()
	defer r.f.pk.Unlock()
	return r.f.parked == nil && !r.f.waiting
}

func (r *parkRig) joinedKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.keys...)
}

func parkOnce(t *testing.T, r *parkRig, key string) {
	t.Helper()
	err := r.f.Login(context.Background(), key, "")
	var p *Parked
	if !errors.As(err, &p) {
		t.Fatalf("want Parked, got %v", err)
	}
	if !strings.Contains(err.Error(), "It will join by itself when the download is done") {
		t.Errorf("message %q", err)
	}
}

func TestJoinDuringDownloadJoinsOnce(t *testing.T) {
	r := newRig(t)
	r.downloading()
	r.f.set(func(s *Status) { s.On = true })
	parkOnce(t, r, keyA)
	if !r.f.State().Joining {
		t.Error("not joining")
	}
	r.finish()
	r.wait(t, "joined", func() bool { return len(r.joinedKeys()) > 0 && r.idle() })
	if k := r.joinedKeys(); len(k) != 1 || k[0] != keyA {
		t.Errorf("joined with %v", k)
	}
	if st := r.f.State(); st.Joining || st.JoinProblem != "" {
		t.Errorf("%+v", st)
	}
}

func TestNewerKeyReplacesParked(t *testing.T) {
	r := newRig(t)
	r.downloading()
	r.f.set(func(s *Status) { s.On = true })
	parkOnce(t, r, keyA)
	parkOnce(t, r, keyB)
	r.finish()
	r.wait(t, "joined", func() bool { return len(r.joinedKeys()) > 0 && r.idle() })
	if k := r.joinedKeys(); len(k) != 1 || k[0] != keyB {
		t.Errorf("joined with %v", k)
	}
}

func TestSwitchOffClearsParked(t *testing.T) {
	r := newRig(t)
	r.downloading()
	r.f.set(func(s *Status) { s.On = true })
	parkOnce(t, r, keyA)
	if err := r.f.SetOn(false); err != nil {
		t.Fatal(err)
	}
	r.wait(t, "idle", r.idle)
	r.finish()
	time.Sleep(50 * time.Millisecond)
	if k := r.joinedKeys(); len(k) != 0 {
		t.Errorf("joined after off: %v", k)
	}
	if r.f.State().Joining {
		t.Error("still joining")
	}
}

func TestParkTimeoutClears(t *testing.T) {
	r := newRig(t)
	parkTimeout = 50 * time.Millisecond
	r.downloading()
	r.f.set(func(s *Status) { s.On = true })
	parkOnce(t, r, keyA)
	r.wait(t, "idle", r.idle)
	st := r.f.State()
	if st.Joining || !strings.Contains(st.JoinProblem, "give the key again") {
		t.Errorf("%+v", st)
	}
	r.finish()
	time.Sleep(50 * time.Millisecond)
	if len(r.joinedKeys()) != 0 {
		t.Error("joined after the timeout")
	}
}

func TestDownloadFailureClearsAndReports(t *testing.T) {
	r := newRig(t)
	r.downloading()
	r.f.set(func(s *Status) { s.On = true })
	parkOnce(t, r, keyA)
	r.f.set(func(s *Status) { s.Downloading, s.Problem = false, "downloading NetBird: boom" })
	r.wait(t, "idle", r.idle)
	st := r.f.State()
	if st.Joining || !strings.Contains(st.JoinProblem, "boom") || !strings.Contains(st.JoinProblem, "give the key again") {
		t.Errorf("%+v", st)
	}
	if len(r.joinedKeys()) != 0 {
		t.Error("joined")
	}
}

// A join that fails after the wait is not held again: the key is asked for.
func TestNoSecondPark(t *testing.T) {
	r := newRig(t)
	r.cliErr = errors.New("exit status 1")
	r.downloading()
	r.f.set(func(s *Status) { s.On = true })
	parkOnce(t, r, keyA)
	r.finish()
	r.wait(t, "idle", func() bool { return r.idle() && !r.f.State().Joining })
	if k := r.joinedKeys(); len(k) != 1 {
		t.Errorf("tried %d times", len(k))
	}
	st := r.f.State()
	if !strings.Contains(st.JoinProblem, "give the key again") {
		t.Errorf("%+v", st)
	}
	if strings.Contains(st.JoinProblem, keyA) {
		t.Error("the key is in the problem")
	}
}

func TestKeyNeverLogged(t *testing.T) {
	r := newRig(t)
	r.cliErr = errors.New("exit status 1")
	r.downloading()
	r.f.set(func(s *Status) { s.On = true })
	parkOnce(t, r, keyA)
	parkOnce(t, r, keyB)
	r.finish()
	r.wait(t, "idle", func() bool { return r.idle() && !r.f.State().Joining })
	log := r.logged()
	if log == "" {
		t.Fatal("nothing logged")
	}
	for _, k := range []string{keyA, keyB} {
		if strings.Contains(log, k) {
			t.Errorf("key in the log:\n%s", log)
		}
	}
	if st := r.f.State(); strings.Contains(statusText(st), keyA) || strings.Contains(st.JoinProblem, keyB) {
		t.Errorf("key in the status: %+v", st)
	}
}
