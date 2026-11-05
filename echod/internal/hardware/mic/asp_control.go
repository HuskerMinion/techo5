package mic

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/HuskerMinion/techo5/echod/internal/asp"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Only the PCM-only ASP session is used in production. Tests substitute
// this narrow interface without ever invoking a privileged device launcher.
type aspProcess interface {
	Exchange(context.Context, [][]byte) ([]byte, error)
	Cancel()
	Close() error
}

type aspAttempt struct {
	ctx      context.Context
	cancel   context.CancelFunc
	process  aspProcess     // published only after READY, protected by Source.mu
	geometry asp.Descriptor // immutable PCM snapshot for this resident child
}
type aspResponse struct {
	attempt *aspAttempt
	pcm     []byte // independently owned by Session.Exchange
}

// BindASP is for the resident echod lifetime, never a capture Run/Close lifetime.
func (s *Source) BindASP(ctx context.Context) {
	s.mu.Lock()
	s.aspParent = ctx
	s.mu.Unlock()
}

func (s *Source) SelectedASP() config.ASPMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aspSelected
}

func (s *Source) ActiveASP() config.ASPMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aspActive
}

func (s *Source) SetASPActiveObserver(f func(config.ASPMode)) {
	s.mu.Lock()
	s.aspActiveChanged = f
	v := s.aspActive
	s.mu.Unlock()
	if f != nil {
		f(v)
	}
}

// activeASPLocked records which processor is supplying audio. The observer (Home Assistant's sensor,
// which writes to every connection) is told from a goroutine of its own, never under s.mu: a slow
// connection must not hold up capture. It is always handed the latest value, so changes in quick
// succession cannot arrive out of order.
func (s *Source) activeASPLocked(v config.ASPMode) {
	if s.aspActive == v {
		return
	}
	s.aspActive = v
	if s.aspActiveChanged == nil {
		return
	}
	if s.aspNotify == nil {
		s.aspNotify = make(chan struct{}, 1)
		go s.notifyASPActive(s.aspNotify)
	}
	select {
	case s.aspNotify <- struct{}{}:
	default: // one is already waiting, and it will read the latest value
	}
}

func (s *Source) notifyASPActive(kick <-chan struct{}) {
	for range kick {
		s.mu.Lock()
		f, v := s.aspActiveChanged, s.aspActive
		s.mu.Unlock()
		if f != nil {
			f(v)
		}
	}
}

// SetASP changes only Selected; HA persists it immediately, independently of
// launching, READY and the first PCM. A failed selection cannot self-retry:
// only a new Built-in -> External transition can reserve another attempt.
func (s *Source) SetASP(v config.ASPMode) config.ASPMode {
	if v != config.ASPExternal {
		v = config.ASPBuiltin
	}
	s.mu.Lock()
	if s.aspSelected == v || s.aspStopped {
		selected := s.aspSelected
		s.mu.Unlock()
		return selected
	}
	old := s.aspCurrent
	s.aspCurrent = nil
	s.aspSelected = v
	s.aspLaunchWanted = v == config.ASPExternal
	s.activeASPLocked(config.ASPBuiltin)
	if old != nil {
		s.endASPLocked(old)
	}
	s.mu.Unlock()
	return v
}

// endASPLocked first invalidates operations, then closes pipes immediately.
// Reaping is an independent job and never waits under the publication lock.
func (s *Source) endASPLocked(a *aspAttempt) {
	a.cancel()
	if a.process == nil {
		return
	}
	p := a.process
	a.process = nil
	p.Cancel()
	s.aspJobs.Add(1) // serialized with shutdown's final Wait by Source.mu
	go func() {
		defer s.aspJobs.Done()
		_ = p.Close() // Session owns the sole cmd.Wait goroutine
		slog.Info("external ASP session termination")
	}()
}

func (s *Source) failASPLocked(a *aspAttempt, err error) {
	if s.aspCurrent != a || s.aspSelected != config.ASPExternal || s.aspStopped || a.ctx.Err() != nil {
		return
	}
	s.aspCurrent = nil
	s.aspLaunchWanted = false
	s.activeASPLocked(config.ASPBuiltin)
	s.endASPLocked(a)
	slog.Warn("external ASP fallback", "reason", err)
}

func (s *Source) failASP(a *aspAttempt, err error) {
	s.mu.Lock()
	s.failASPLocked(a, err)
	s.mu.Unlock()
}

// captureNative runs in the synchronous pre-conversion native tap. A launch
// captures only descriptor geometry, never borrowed PCM. READY is awaited in
// a goroutine while Built-in frames and stock sensors continue flowing.
func (s *Source) captureNative(block NativeBlock) *aspResponse {
	s.mu.Lock()
	if s.aspStopped || s.aspSelected != config.ASPExternal || s.aspParent == nil || s.aspParent.Err() != nil {
		s.mu.Unlock()
		return nil
	}
	if s.aspCurrent == nil && s.aspLaunchWanted {
		s.aspLaunchWanted = false
		ctx, cancel := context.WithCancel(s.aspParent)
		a := &aspAttempt{ctx: ctx, cancel: cancel}
		s.aspCurrent = a
		d := block.Descriptor()
		// Take the geometry's own independent snapshot before the next read.
		d.Buffers = append([]asp.Buffer(nil), d.Buffers...)
		d.Channels = append([]asp.Channel(nil), d.Channels...)
		a.geometry = d
		opener := s.aspOpen
		s.aspJobs.Add(1)
		go func() {
			defer s.aspJobs.Done()
			slog.Info("external ASP launch")
			p, err := opener(ctx, d)
			if err != nil {
				s.failASP(a, fmt.Errorf("launch/READY: %w", err))
				return
			}
			s.mu.Lock()
			if s.aspCurrent == a && s.aspSelected == config.ASPExternal && !s.aspStopped && ctx.Err() == nil {
				a.process = p
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			p.Cancel()
			_ = p.Close() // stale launch never touches a newer session
			slog.Info("external ASP session termination")
		}()
	}
	a := s.aspCurrent
	// Reopened capture may retain buffer lengths but change sample interpretation.
	// Never send PCM under the descriptor pinned at an earlier READY.
	if a != nil && (!slices.Equal(a.geometry.Buffers, block.Geometry.Buffers) ||
		!slices.Equal(a.geometry.Channels, block.Geometry.Channels)) {
		s.failASPLocked(a, errors.New("native PCM geometry changed"))
		s.mu.Unlock()
		return nil
	}
	if a == nil || a.process == nil || a.ctx.Err() != nil {
		s.mu.Unlock()
		return nil
	}
	p := a.process
	s.mu.Unlock()

	pcm, err := p.Exchange(a.ctx, block.Buffers)
	if err != nil {
		s.failASP(a, fmt.Errorf("PCM exchange: %w", err))
		return nil
	}
	if len(pcm) != asp.OutputBytes {
		s.failASP(a, errors.New("short External PCM frame"))
		return nil
	}
	return &aspResponse{attempt: a, pcm: pcm}
}

// aspFrameLocked is called AFTER stock DSP/sensors and under the SAME lock as
// mode changes and history/spectrum/listener publication. No blocking IPC or
// reaper wait holds this lock; stale results can never pass final publication.
func (s *Source) aspFrameLocked(r *aspResponse, stock []int16) []int16 {
	if r == nil || s.aspSelected != config.ASPExternal || s.aspStopped ||
		s.aspCurrent != r.attempt ||
		r.attempt.ctx.Err() != nil || len(r.pcm) != asp.OutputBytes {
		return stock
	}
	out := make([]int16, asp.OutputBytes/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(r.pcm[2*i:]))
	}
	s.activeASPLocked(config.ASPExternal)
	return out
}

// StopASP is called BEFORE service.Group waits for Run, including early
// required-service failures. Capture Close, Rewire and XRUN never call it.
func (s *Source) StopASP() {
	s.mu.Lock()
	if !s.aspStopped {
		s.aspStopped = true
		s.aspLaunchWanted = false
		if s.aspCurrent != nil {
			s.endASPLocked(s.aspCurrent)
			s.aspCurrent = nil
		}
		s.activeASPLocked(config.ASPBuiltin)
	}
	s.mu.Unlock()
}

// WaitASP runs only after Group has finished waiting for capture Run.
// It joins launch/cleanup jobs; Session.Close joins the sole child reaper.
func (s *Source) WaitASP() {
	s.StopASP()
	s.aspJobs.Wait()
}

// A backend may supply stock PCM but no representable native descriptor.
// This fails a single External attempt without stopping capture or retrying.
func (s *Source) failNative(err error) {
	s.mu.Lock()
	if s.aspSelected == config.ASPExternal && !s.aspStopped && (s.aspLaunchWanted || s.aspCurrent != nil) {
		s.aspLaunchWanted = false
		if a := s.aspCurrent; a != nil {
			s.aspCurrent = nil
			s.endASPLocked(a)
		}
		s.activeASPLocked(config.ASPBuiltin)
		slog.Warn("external ASP fallback", "reason", err)
	}
	s.mu.Unlock()
}
