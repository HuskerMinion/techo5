package mic

import (
	"log/slog"
	"math"
	"sync/atomic"

	"github.com/HuskerMinion/techo5/echod/internal/lib/aec"
	"github.com/HuskerMinion/techo5/echod/internal/lib/audio"
)

// cancelTaps is how far the filter reaches, in samples, so 64 ms of tail. Not a setting: measured on
// this enclosure, cancellation rises with every tap available — 16.5 dB at 256, 18.6 at 512, 23.0 at
// 1024, 27.2 at 2048 — so there is no room-dependent best value to look for, only what the device can
// afford. This costs 6.4% of a core, and only while something is playing.
const cancelTaps = 1024

// cancelMu is how fast the filter adapts. Fast enough to converge inside the first second of a reply,
// which matters because a reply is all the time there is.
const cancelMu = 0.5

// refQuiet is the mean square per sample, at int16 scale, below which the loopback counts as silence.
// About -76 dBFS. Below it there is no echo to remove, so the filter is skipped entirely and the frame
// costs nothing — which is what keeps this free on an idle device. It has to sit well under a
// radio at a quiet volume: measured at -50 dBFS on the loopback, which a -60 dBFS gate flapped on
// every second, resetting the canceller each time.
const refQuiet = 1e-7 * 32768 * 32768 / 4

// refHold is how long the filter keeps running after the loopback goes quiet, in samples.
//
// Speech is full of gaps, and without this the gate flaps between every word. That is not only untidy:
// the room is still ringing with the echo of the word that just played, and the measured tail here runs
// past 100 ms, so disengaging on the gap stops canceling exactly as that tail arrives. A second
// covers it with room to spare, and rides across the pauses in a radio talk show.
const refHold = Rate

// canceller subtracts the playback loopback from one fixed acoustic path, and replaces the mix while it
// runs.
//
// The path it reads cannot move: the filter learns one path from the speaker, and the beamformer steers
// at the loudest sound, which during playback is our own speaker. Steering into the echo is the opposite
// of useful when the echo is what is being removed. A fixed combination of microphones is as fixed as one
// microphone, though, because the microphones do not move: on the Dot, the average of seven canceled
// once measured exactly as well as seven cancellers averaged, and 5 dB better than the center microphone
// alone. Which path it reads is cancelInput's choice.
type canceller struct {
	filter *aec.Canceller

	// ext is WebRTC's canceller in a helper process (webrtc.go), used instead of filter while it is
	// set and healthy. engine names which one the user asked for.
	ext    *external
	engine string

	// ref and mono are reused every frame; decoding into them keeps the audio path free of allocation.
	ref  []int16
	mono []int16

	// erle is published for diagnostics, as thousandths of a dB so it fits an integer. best is the most
	// it reached during the current run: ERLE is averaged over the last half second, so reading it as
	// playback ends catches the reference fading out and says the filter did worse than it did.
	erle atomic.Int64
	best atomic.Int64

	// active says whether the last frame had playback in it, which is when any of this happened.
	active atomic.Bool

	// hold counts down the samples still to filter after the loopback went quiet. Reader-only.
	hold int
}

func newCanceller() *canceller {
	f, err := aec.New(aec.Config{Taps: cancelTaps, Mu: cancelMu})
	if err != nil {
		// Only a bad Taps or Mu reaches this, and both are constants above.
		slog.Error("echo cancellation unavailable", "err", err)
		return nil
	}
	return &canceller{filter: f, engine: "builtin"}
}

// setEngine picks the canceller: "builtin" is the LMS filter above, "webrtc" the helper process.
// It reports what it settled on: webrtc falls back to builtin when the helper cannot start.
func (c *canceller) setEngine(name string) string {
	if c.ext != nil {
		c.ext.Close()
		c.ext = nil
	}
	c.engine = "builtin"
	if name != "webrtc" {
		return c.engine
	}
	e, err := startExternal("--ns", "low")
	if err != nil {
		slog.Warn("webrtc echo cancellation unavailable, using the built-in filter", "err", err)
		return c.engine
	}
	c.ext, c.engine = e, "webrtc"
	return c.engine
}

// process runs one block through whichever engine is on. A helper that has died is dropped for
// the built-in filter, once, with a log line.
func (c *canceller) process(mic, ref []int16) ([]int16, float64, error) {
	if c.ext != nil {
		if c.ext.Healthy() {
			out, err := c.ext.Process(mic, ref)
			if err == nil {
				return out, c.ext.ERLE(), nil
			}
			slog.Warn("webrtc echo cancellation failed, using the built-in filter", "err", err)
		}
		c.ext.Close()
		c.ext = nil
		c.engine = "builtin"
	}
	out, err := c.filter.Process(mic, ref)
	if err != nil {
		return nil, 0, err
	}
	return out, c.filter.ERLE(), nil
}

// apply returns mic, one fixed path (see cancelInput), with the echo removed, or nil when there is
// nothing playing and the caller should use the mix it already has.
func (c *canceller) apply(raw []byte, mic []int16) []int16 {
	n := len(mic)
	if cap(c.ref) < n {
		c.ref = make([]int16, n)
	}
	c.ref = c.ref[:n]
	referenceInto(raw, c.ref)

	switch {
	case playing(c.ref):
		c.hold = refHold
	case c.hold > 0:
		// Still inside the tail of what just played.
		c.hold -= n
	default:
		// Nothing to cancel. The filter keeps what it learned: the room has not changed just because
		// the reply ended, so the next one starts converged rather than from nothing.
		if c.active.Swap(false) {
			slog.Info("echo cancellation idle",
				"best_db", float64(c.best.Load())/1000, "last_db", float64(c.erle.Load())/1000)
			c.erle.Store(0)
			c.best.Store(0)
		}
		return nil
	}

	if !c.active.Swap(true) {
		slog.Info("echo cancellation running", "engine", c.engine, "taps", cancelTaps,
			"ref_dbfs", blockDBFS(c.ref), "mic_dbfs", blockDBFS(mic))
	}

	out, erleDB, err := c.process(mic, c.ref)
	if err != nil {
		slog.Error("echo cancellation failed", "err", err)
		return nil
	}
	erle := int64(erleDB * 1000)
	c.erle.Store(erle)
	if erle > c.best.Load() {
		c.best.Store(erle)
	}

	// The engines hand back a buffer of their own that the next block overwrites, and the caller runs
	// the denoiser and the leveler over what this returns, so the block is moved somewhere they may
	// write on. This buffer is reused as well: what apply returns is only good until the next call,
	// and broadcast copies it before any listener keeps it.
	if cap(c.mono) < len(out) {
		c.mono = make([]int16, len(out))
	}
	c.mono = c.mono[:len(out)]
	copy(c.mono, out)
	return c.mono
}

// blockDBFS is the RMS of a block in dBFS, rounded, for the log.
func blockDBFS(s []int16) float64 {
	if len(s) == 0 {
		return -120
	}
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	rms := math.Sqrt(sum / float64(len(s)))
	if rms < 1 {
		return -120
	}
	return math.Round(20*math.Log10(rms/32768)*10) / 10
}

// playing reports whether the loopback carries anything.
func playing(ref []int16) bool {
	var sum float64
	for _, v := range ref {
		sum += float64(v) * float64(v)
	}
	return len(ref) > 0 && sum/float64(len(ref)) > refQuiet
}

// referenceInto decodes ch7, the left half of the playback loopback, into dst. ch8 is left alone: on
// music the two measure within 12-17 dB of each other, which caps cancellation far above anything the
// filter reaches, so a stereo reference would buy nothing.
func referenceInto(raw []byte, dst []int16) {
	frameBytes := Channels * Bits / 8

	frames := min(len(raw)/frameBytes, len(dst))
	for f := range frames {
		o := f*frameBytes + RefFirst*3
		dst[f] = int16(audio.DecodeS24LE3(raw[o:o+3]) >> 8)
	}
	for f := frames; f < len(dst); f++ {
		dst[f] = 0
	}
}

// Canceling reports whether the canceller is currently running, which it does only while the loopback
// is carrying audio.
func (s *Source) Canceling() bool {
	return s.cancel != nil && s.cancel.active.Load()
}

// ERLE is how much echo the canceller is removing, in dB, or zero when it is not running.
func (s *Source) ERLE() float64 {
	if s.cancel == nil {
		return 0
	}
	return float64(s.cancel.erle.Load()) / 1000
}

// SetCanceling turns echo cancellation on or off. Off is the same signal path the device had before it
// existed, which is what makes it worth having as a switch: it is the comparison.
func (s *Source) SetCanceling(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.canceling = on
}

// SetCancelEngine chooses which canceller runs — "builtin" or "webrtc" — and reports what it
// settled on. Serialized with the capture loop, since the engine is used from there.
func (s *Source) SetCancelEngine(name string) string {
	if s.cancel == nil {
		return "builtin"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel.setEngine(name)
}

// CancelEngine is which canceller is running.
func (s *Source) CancelEngine() string {
	if s.cancel == nil {
		return "builtin"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel.engine
}

// SetAdapting stops or resumes the canceller learning, while it goes on canceling with what it has.
//
// Turn it off while somebody is being listened to. The filter cannot tell a voice it was never given a
// reference for from an echo it predicted badly, so it treats the voice as its own error and fits itself
// to it — at exactly the moment canceling matters. What it already learned stays correct: the room did
// not change because somebody spoke.
func (s *Source) SetAdapting(on bool) {
	if s.cancel != nil {
		s.cancel.filter.SetAdapting(on)
	}
}
