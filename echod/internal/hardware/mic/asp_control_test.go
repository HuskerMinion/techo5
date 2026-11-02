package mic

import (
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/asp"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

type aspTestProcess struct {
	once        sync.Once
	canceled    chan struct{}
	done        chan struct{}
	wait        <-chan struct{}
	value       int16
	entered     chan struct{}
	fail        error
	honorCancel bool
}

func aspTestFake(v int16) *aspTestProcess {
	return &aspTestProcess{canceled: make(chan struct{}), done: make(chan struct{}), value: v}
}
func (p *aspTestProcess) Exchange(ctx context.Context, bufs [][]byte) ([]byte, error) {
	if p.entered != nil {
		select {
		case p.entered <- struct{}{}:
		default:
		}
	}
	if p.fail != nil {
		return nil, p.fail
	}

	if p.wait != nil {
		if p.honorCancel {
			select {
			case <-p.wait:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else {
			<-p.wait
		} // deliberately hostile, simulates stale reply
	}
	b := make([]byte, asp.OutputBytes)
	for i := 0; i < len(b); i += 2 {
		binary.LittleEndian.PutUint16(b[i:], uint16(p.value))
	}
	return b, nil
}
func (p *aspTestProcess) Cancel() { p.once.Do(func() { close(p.canceled) }) }
func (p *aspTestProcess) Close() error {
	p.Cancel()
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	return nil
}

func aspTestBlock() NativeBlock {
	b := make([]byte, FrameSamples*Channels*3)
	return nativeInterleaved(b, 16000, asp.FormatS24_3, 24, 0, Mics, Refs)
}
func aspTestStock() []byte {
	return make([]byte, FrameSamples*Channels*3)
}
func aspTestAwait(t *testing.T, pred func() bool) {
	t.Helper()
	end := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !pred() {
		select {
		case <-end:
			t.Fatal("ASP startup/cleanup did not finish")
		case <-ticker.C:
		}
	}
}
func aspTestReady(s *Source) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aspCurrent != nil && s.aspCurrent.process != nil
}
func aspTestSource(t *testing.T, opener func(context.Context, asp.Descriptor) (aspProcess, error)) *Source {
	t.Helper()
	s := New()
	s.aspOpen = opener
	s.BindASP(context.Background())
	if got := s.SetASP(config.ASPExternal); got != config.ASPExternal {
		t.Fatal(got)
	}
	t.Cleanup(func() { s.StopASP(); s.WaitASP() })
	return s
}

// A capture reopen may change PCM meaning without changing the wire byte count.
// The resident helper must never process samples using a stale descriptor.
func TestCaptureReopenGeometryChangeFallsBack(t *testing.T) {
	p := aspTestFake(71)
	var launches atomic.Int32
	s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) {
		launches.Add(1)
		return p, nil
	})
	if s.captureNative(aspTestBlock()) != nil {
		t.Fatal("launch must be asynchronous")
	}
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	if s.captureNative(aspTestBlock()) == nil {
		t.Fatal("same geometry unexpectedly reset session")
	}
	changed := aspTestBlock()
	changed.Geometry.Buffers[0].SampleRate = 48000 // same buffers, different physical PCM geometry
	if s.captureNative(changed) != nil {
		t.Fatal("stale descriptor accepted capture-reopened PCM")
	}
	if s.SelectedASP() != config.ASPExternal || s.ActiveASP() != config.ASPBuiltin {
		t.Fatal("geometry mismatch must fall back while retaining selection")
	}
	select {
	case <-p.canceled:
	default:
		t.Fatal("stale resident helper not canceled")
	}
	if s.captureNative(aspTestBlock()) != nil || launches.Load() != 1 {
		t.Fatal("geometry mismatch retried without manual reactivation")
	}
}

func TestSlowREADYAndFirstPCMActive(t *testing.T) {
	ready := make(chan struct{})
	p := aspTestFake(1642)
	s := aspTestSource(t, func(ctx context.Context, d asp.Descriptor) (aspProcess, error) {
		select {
		case <-ready:
			return p, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	ch, unsub := s.Listen("aspTest")
	defer unsub()
	if got := s.captureNative(aspTestBlock()); got != nil {
		t.Fatal("launch blocked capture")
	}
	s.broadcastSelected(aspTestStock(), false, nil)
	if len(<-ch) != FrameSamples || s.ActiveASP() != config.ASPBuiltin {
		t.Fatal("Built-in not running during READY")
	}
	close(ready)
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	r := s.captureNative(aspTestBlock())
	s.broadcastSelected(aspTestStock(), false, r)
	f := <-ch
	if r == nil || len(f) != FrameSamples || f[0] != 1642 || s.ActiveASP() != config.ASPExternal || s.SelectedASP() != config.ASPExternal {
		t.Fatal("first External output failed")
	}
	h := s.Recent(20 * time.Millisecond)
	if !slices.Equal(h, f) {
		t.Fatal("history differs from listeners")
	}
}

func TestFallbackNoRetryAndManualReactivation(t *testing.T) {
	var launches atomic.Int32
	s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) {
		launches.Add(1)
		if launches.Load() == 1 {
			return nil, errors.New("missing executable")
		}
		return aspTestFake(803), nil
	})
	if s.captureNative(aspTestBlock()) != nil {
		t.Fatal("launch should be async")
	}
	aspTestAwait(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.aspCurrent == nil && !s.aspLaunchWanted
	})
	for i := 0; i < 5; i++ {
		s.captureNative(aspTestBlock())
	}
	if launches.Load() != 1 || s.ActiveASP() != config.ASPBuiltin || s.SelectedASP() != config.ASPExternal {
		t.Fatal("fallback retried or overwrote Selected")
	}
	s.SetASP(config.ASPBuiltin)
	s.SetASP(config.ASPExternal)
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	if launches.Load() != 2 {
		t.Fatal("manual retry did not re-evaluate executable")
	}
}

func TestOldLaunchCannotReplaceNewAndStop(t *testing.T) {
	oldReady := make(chan struct{})
	first := aspTestFake(11)
	second := aspTestFake(22)
	var calls atomic.Int32
	s := aspTestSource(t, func(ctx context.Context, d asp.Descriptor) (aspProcess, error) {
		calls.Add(1)
		if calls.Load() == 1 {
			// Deliberately ignore cancellation: late old READY.
			<-oldReady
			return first, nil
		}
		return second, nil
	})
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return calls.Load() == 1 })
	s.SetASP(config.ASPBuiltin)
	s.SetASP(config.ASPExternal)
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	close(oldReady)
	aspTestAwait(t, func() bool {
		select {
		case <-first.done:
			return true
		default:
			return false
		}
	})
	r := s.captureNative(aspTestBlock())
	if r == nil || r.attempt.process != second {
		t.Fatal("stale launch replaced current session")
	}
	s.StopASP()
	select {
	case <-second.canceled:
	default:
		t.Fatal("shutdown failed to cancel current process")
	}
	s.WaitASP()
}

func TestStalePublicationAndExternalOwnership(t *testing.T) {
	p := aspTestFake(1593)
	s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) { return p, nil })
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	out, unsub := s.Listen("owned")
	defer unsub()
	r := s.captureNative(aspTestBlock())
	if r == nil {
		t.Fatal("no External response")
	}
	s.broadcastSelected(aspTestStock(), false, r)
	a := <-out
	saved := slices.Clone(a)
	for i := 0; i < 3; i++ {
		s.broadcastSelected(aspTestStock(), false, s.captureNative(aspTestBlock()))
		<-out
	}
	if !slices.Equal(a, saved) || a[0] != 1593 {
		t.Fatal("old External output mutated")
	}
	late := s.captureNative(aspTestBlock())
	s.SetASP(config.ASPBuiltin)
	s.broadcastSelected(aspTestStock(), false, late)
	stale := <-out
	if stale[0] == 1593 || s.ActiveASP() != config.ASPBuiltin || !slices.Equal(a, saved) {
		t.Fatal("stale External result published after selection changed")
	}
	if h := s.Recent(20 * time.Millisecond); h[0] == 1593 {
		t.Fatal("stale External leaked into history")
	}
}

func TestCloseDoesNotCancelSession(t *testing.T) {
	p := aspTestFake(37)
	s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) { return p, nil })
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.canceled:
		t.Fatal("capture Close killed resident helper")
	default:
	}
	if s.captureNative(aspTestBlock()) == nil {
		t.Fatal("reopened capture cannot use same helper")
	}
	s.StopASP()
	select {
	case <-p.canceled:
	default:
		t.Fatal("final stop did not cancel")
	}
	if err := s.Start(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("new capture accepted after stop: %v", err)
	}
}

func TestFinalStopUnblocksExchangeBeforeWait(t *testing.T) {
	block := make(chan struct{})
	p := aspTestFake(4)
	p.wait = block
	p.honorCancel = true
	s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) { return p, nil })
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	done := make(chan struct{})
	go func() { defer close(done); s.captureNative(aspTestBlock()) }()
	// The Exchange may race with shutdown, but must not prevent reaping.
	s.StopASP()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked exchange survived cancellation")
	}
	s.WaitASP()
	select {
	case <-p.done:
	default:
		t.Fatal("sole process cleanup not joined")
	}
}

func TestNativeSoftwareCutAndStockRaw(t *testing.T) {
	raw := aspTestStock()
	for i := range raw {
		raw[i] = 0x6b
	}
	// The stock buffer includes the physical MIC and REF slots, not just Mics.
	cfg := alsaConfig()
	cfg.Channels = Channels
	fake := &fakePlainCapture{cfg: cfg, bytes: raw}
	dst := make([]byte, len(raw))
	visited := false
	_, cut, err := readCaptured(fake, dst, func() bool { return true }, func(b NativeBlock) {
		visited = true
		if len(b.Geometry.Channels) != Channels || len(b.Buffers) != 1 {
			t.Fatalf("native geometry lost physical MIC/REF: %+v", b.Geometry)
		}
		for i, ch := range b.Geometry.Channels {
			wantRole, wantSlot := asp.RoleMIC, i
			if i >= Mics {
				wantRole, wantSlot = asp.RoleREF, i-Mics
			}
			if ch.Role != wantRole || ch.RoleIndex != uint16(wantSlot) || ch.FrameOffset != uint32(i*3) {
				t.Fatalf("physical MIC/REF %d has wrong role/slot/offset: %+v", i, ch)
			}
		}
		// All physical channels are zeroed, including distinct REF L/R on two-REF devices.
		for _, native := range b.Buffers {
			for _, v := range native {
				if v != 0 {
					t.Fatal("native MIC/REF not muted")
				}
			}
		}
	})
	if err != nil || !cut || !visited {
		t.Fatal("cut native capture failed", err)
	}
	for _, v := range dst {
		if v != 0 {
			t.Fatal("stock mute changed")
		}
	}
}

// The stock capture reader can report an ALSA overrun and then exit. Neither
// situation may terminate/relaunch the resident helper (Rewire uses Close).
type aspTestOverrunCapture struct{ reads int }

func (c *aspTestOverrunCapture) Read(buf []byte) (int, error) {
	c.reads++
	if c.reads == 1 {
		return 0, alsa.ErrOverrun
	}
	return 0, errors.New("simulated capture failure")
}
func (*aspTestOverrunCapture) Close() error        { return nil }
func (*aspTestOverrunCapture) Config() alsa.Config { return alsaConfig() }
func TestRunExitAndXRUNPreserveResidentProcess(t *testing.T) {
	p := aspTestFake(55)
	s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) { return p, nil })
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	capture := &aspTestOverrunCapture{}
	s.devMu.Lock()
	s.pcm = capture
	s.devMu.Unlock()
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("capture failure suppressed")
	}
	if capture.reads != 2 {
		t.Fatalf("xrun should continue stock capture, got %d reads", capture.reads)
	}
	select {
	case <-p.canceled:
		t.Fatal("capture Run/overrun terminated helper")
	default:
	}
	if got := s.captureNative(aspTestBlock()); got == nil {
		t.Fatal("resident session was not preserved")
	}
}

// A malicious/late PCM reply from a canceled old process must lose against a
// fresh manual session, even when its Exchange ignores cancellation entirely.
func TestLatePCMReadAfterManualReplacement(t *testing.T) {
	release := make(chan struct{})
	old := aspTestFake(111)
	old.wait = release
	old.entered = make(chan struct{}, 1)
	fresh := aspTestFake(222)
	var launched atomic.Int32
	s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) {
		if launched.Add(1) == 1 {
			return old, nil
		}
		return fresh, nil
	})
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	ch, unsub := s.Listen("late-source")
	defer unsub()
	staleDone := make(chan *aspResponse, 1)
	go func() { staleDone <- s.captureNative(aspTestBlock()) }()
	select {
	case <-old.entered:
	case <-time.After(time.Second):
		t.Fatal("old exchange never started")
	}
	s.SetASP(config.ASPBuiltin)
	s.SetASP(config.ASPExternal)
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	close(release)
	var stale *aspResponse
	select {
	case stale = <-staleDone:
	case <-time.After(time.Second):
		t.Fatal("stale exchange blocked")
	}
	s.broadcastSelected(aspTestStock(), false, stale)
	if got := <-ch; got[0] == 111 || s.ActiveASP() != config.ASPBuiltin {
		t.Fatal("stale PCM published")
	}
	r := s.captureNative(aspTestBlock())
	s.broadcastSelected(aspTestStock(), false, r)
	if got := <-ch; got[0] != 222 || s.ActiveASP() != config.ASPExternal {
		t.Fatal("old cleanup replaced current helper")
	}
}

func TestSwitchCancelsBlockedPCM(t *testing.T) {
	blocked := make(chan struct{})
	p := aspTestFake(23)
	p.wait = blocked
	p.honorCancel = true
	p.entered = make(chan struct{}, 1)
	s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) { return p, nil })
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	finished := make(chan *aspResponse, 1)
	go func() { finished <- s.captureNative(aspTestBlock()) }()
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("exchange not entered")
	}
	s.SetASP(config.ASPBuiltin)
	select {
	case response := <-finished:
		if response != nil {
			t.Fatal("canceled PCM accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("mode change did not unblock PCM")
	}
	if s.ActiveASP() != config.ASPBuiltin {
		t.Fatal("late activation")
	}
}

func TestPCMFailureFallbackAndManualOnlyRetry(t *testing.T) {
	var starts atomic.Int32
	s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) {
		starts.Add(1)
		p := aspTestFake(31)
		p.fail = errors.New("simulated helper EOF")
		return p, nil
	})
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	if r := s.captureNative(aspTestBlock()); r != nil {
		t.Fatal("failed exchange published")
	}
	if s.ActiveASP() != config.ASPBuiltin || s.SelectedASP() != config.ASPExternal {
		t.Fatal("fallback altered Selected/Active")
	}
	for i := 0; i < 4; i++ {
		s.captureNative(aspTestBlock())
	}
	if starts.Load() != 1 {
		t.Fatal("automatic retry after PCM failure")
	}
	s.SetASP(config.ASPBuiltin)
	s.SetASP(config.ASPExternal)
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	if starts.Load() != 2 {
		t.Fatal("manual retry did not launch fresh session")
	}
}

type aspTestPartialCapture struct{ cause error }

func (c *aspTestPartialCapture) Read(dst []byte) (int, error) {
	copy(dst, []byte{1, 2, 3, 4, 5, 6})
	return 6, c.cause
}
func (*aspTestPartialCapture) Close() error        { return nil }
func (*aspTestPartialCapture) Config() alsa.Config { return alsaConfig() }

func TestPartialHardwareReadIsNotExternalGeometryFallback(t *testing.T) {
	p := aspTestFake(18)
	s := aspTestSource(t, func(context.Context, asp.Descriptor) (aspProcess, error) { return p, nil })
	s.captureNative(aspTestBlock())
	aspTestAwait(t, func() bool { return aspTestReady(s) })
	failure := errors.New("actual capture driver failed")
	s.devMu.Lock()
	s.pcm = &aspTestPartialCapture{cause: failure}
	s.devMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Run(ctx); !errors.Is(err, failure) {
		t.Fatalf("capture read error masked as External geometry: %v", err)
	}
	select {
	case <-p.canceled:
		t.Fatal("capture failure incorrectly killed resident ASP")
	default:
	}
}
