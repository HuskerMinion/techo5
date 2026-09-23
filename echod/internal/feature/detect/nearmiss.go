package detect

import (
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Ducking after a near miss, so the second try is heard.
//
// Measured on an Echo Dot 2 (techo5-dot docs/microphones.md): over playback the wake word stops
// firing once the echo at the microphones passes about -60 dBFS, and no filter recovers it — what the
// canceller leaves behind is the speaker's own distortion, which the reference it subtracts cannot
// contain. Ducking as the score rises was the obvious answer and does not work: the score passes
// NearMiss only 120-140 ms before it would fire, by which time the word is over.
//
// The near miss itself is the usable signal. Somebody said it, the device almost heard it, and they
// are about to say it again — so the music gets out of the way for a few seconds. It costs nothing
// when nothing is playing, and a false near miss costs a few seconds of quieter music.
const (
	// nearMissDuck is how long the music stays down: long enough to say it again after hearing the
	// music drop, short enough not to read as a fault.
	nearMissDuck = 4 * time.Second

	// nearMissQuiet is how long after a duck before another near miss may start one, so a room that
	// keeps nearly triggering does not hold the music down for ever.
	nearMissQuiet = 6 * time.Second

	// ringHushQuiet is the same idea for a ring, and it matters more: music held down is an
	// annoyance, where an alarm held down is the failure this whole plan exists to prevent. Three
	// times the hush, so at most a third of a ringing alarm can be quiet however hard a noisy room
	// tries.
	ringHushQuiet = 3 * ring.HushFor
)

// ducker holds a playing track down for a while after a near miss.
//
// Everything it touches is a field, because what this is worth is in when it ducks and when it
// declines to, and that is only testable if the speaker and the turn can be stood in for.
type ducker struct {
	mu sync.Mutex

	// until is when the current duck ends; again is the earliest a new one may start.
	until time.Time
	again time.Time

	// ringAgain is the earliest a new ring hush may start. The hush's own end is kept by the ring,
	// which expires it by itself, so there is no deadline to track here — only a floor on how often.
	ringAgain time.Time

	// stop ends the current duck early enough to be extended, kept so a second near miss inside a duck
	// pushes the end out rather than leaving two timers racing to undo it.
	stop interface{ Reset(time.Duration) bool }

	// enabled, playing, busy, duck, ringing and hush are the world; now and after are the clock.
	enabled func() bool
	playing func() bool
	busy    func() bool
	duck    func(bool)
	ringing func() bool
	hush    func()
	now     func() time.Time
	after   func(time.Duration, func()) interface{ Reset(time.Duration) bool }
}

// newDucker wires a ducker to the device.
func newDucker() *ducker {
	return &ducker{
		enabled: func() bool { return config.Get().Media.DuckOnNearMiss },
		// The arbiter, rather than the player: the question is whether anything of ours is audible.
		playing: func() bool { return speaker.Sound().Backgrounds().Playing() != nil },
		busy:    func() bool { return voice.Get().Busy() },
		duck:    func(on bool) { speaker.Sound().Backgrounds().Duck(on) },
		ringing: ring.IsSounding,
		hush:    ring.Hush,
		now:     time.Now,
		after: func(d time.Duration, f func()) interface{ Reset(time.Duration) bool } {
			return time.AfterFunc(d, f)
		},
	}
}

// heard is called on every near miss.
func (d *ducker) heard(slot int, peak float64) {
	// A ring is handled first and on its own terms. It is the stop word only, because that is the
	// word somebody says at a ringing alarm, and it is not gated on the media setting: that setting
	// is about music getting out of the way, where this is about an alarm being stoppable at all.
	if slot == StopSlot && d.ringing() {
		d.hushRing(peak)
	}

	if !d.enabled() || !d.playing() {
		return
	}

	now := d.now()

	d.mu.Lock()
	switch {
	case now.Before(d.until):
		// Already down: push the end out without restarting the cycle.
		d.until = now.Add(nearMissDuck)
		if d.stop != nil {
			d.stop.Reset(nearMissDuck)
		}
		d.mu.Unlock()
		slog.Info("near miss while ducked, holding the music down", "slot", slot+1, "peak", peak)
		return
	case now.Before(d.again):
		d.mu.Unlock()
		return
	}

	d.until = now.Add(nearMissDuck)
	d.stop = d.after(nearMissDuck, d.restore)
	d.mu.Unlock()

	slog.Info("near miss over playback, ducking so the next try is heard",
		"slot", slot+1, "peak", peak, "for", nearMissDuck)
	d.duck(true)
}

// hushRing holds the chime back so the next try at the stop word lands in a gap, no more often than
// ringHushQuiet.
func (d *ducker) hushRing(peak float64) {
	now := d.now()

	d.mu.Lock()
	if now.Before(d.ringAgain) {
		d.mu.Unlock()
		return
	}
	d.ringAgain = now.Add(ringHushQuiet)
	d.mu.Unlock()

	slog.Info("near miss on the stop word over a ring, hushing the chime so the next try is heard",
		"peak", peak, "for", ring.HushFor)
	d.hush()
}

// restore puts the level back, unless a turn is holding it down: a near miss that becomes a real
// detection ducks for the turn, and that duck outlives this one.
func (d *ducker) restore() {
	d.mu.Lock()
	d.until = time.Time{}
	d.again = d.now().Add(nearMissQuiet)
	d.stop = nil
	d.mu.Unlock()

	if d.busy() {
		slog.Debug("near miss duck expired during a turn, leaving the level to the turn")
		return
	}
	d.duck(false)
}

// watch wires the ducker to an engine.
func (d *ducker) watch(e *Engine) {
	e.OnNearMiss = func(slot int, peak float64) {
		safe.Go("near miss duck", func() { d.heard(slot, peak) })
	}
}
