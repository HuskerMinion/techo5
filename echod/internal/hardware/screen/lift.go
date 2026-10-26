//go:build !dot && !panel

package screen

import (
	"errors"
	"log/slog"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/gpu"
)

// The lift (Adaptive backlight): on dark content the backlight is turned down and every pixel drawn
// brighter by as much, so the panel shows the same picture for less light. An LCD's light is the
// backlight times each pixel's transmission, and the panel's transmission follows its code to the power
// 2.2: a backlight at raw/asked of what was asked and a gain of (asked/raw)^(1/2.2) on every code
// cancel exactly. Nothing is lost while the canvas's brightest red, green or blue value times the gain
// stays at 255 or under, so that value (measured by techo5-gpu as it draws, 0.6 ms a frame on the Show
// 8) sets how far the backlight goes down: a canvas reaching 233 (the theme's text) gives 18%, one with
// the weather's sun (247) 7%, and anything with white in it nothing.
//
// The gain is drawn by the GPU into the frame it belongs to (dithered, so it cannot band), and the
// backlight is written as soon as that frame's page flip is done: the two change within a millisecond
// of each other, at most a frame's top rows apart. Both move slowly, in steps of one raw backlight
// level: down at liftDown of the backlight a second, after the content has been as dark for liftHold;
// back up at liftUp a second as soon as brighter content needs the room. What appears brighter than
// the room left for it is held at 255 for the few frames that takes (a sheet sliding in, a page
// changing), never anything already on the panel.
//
// It works only where the GPU draws the panel. A video or camera frame is not measured, so the lift
// goes back to none under it. Under liftMinRaw of the driver's range (the night's panel) it stays
// off: the steps are too coarse there and the LED's light is least linear in its duty.

const (
	liftGamma = 2.2
	// liftFloor is the most the backlight is turned down, whatever the content: half.
	liftFloor = 0.5
	// liftMinRaw is the asked level, out of 1023, under which the lift stays off.
	liftMinRaw = 40
	// liftHold is how long the content must stay darker before the backlight goes down for it.
	liftHold = 10 * time.Second
	// liftDown and liftUp are how fast the backlight moves, as fractions of the asked level a second.
	liftDown = 0.04
	liftUp   = 1.0
	// liftTick is how often a ramp steps while nothing else is drawn.
	liftTick = 33 * time.Millisecond
	// liftReport is how often the saving is logged.
	liftReport = time.Hour
	// liftBackoff is how long the lift rests after the helper failed a step, doubling with each failure
	// in a row up to liftBackoffMax: no step is tried meanwhile, so a failing helper is not asked again
	// at every tick, each time with the lock held.
	liftBackoff    = 10 * time.Second
	liftBackoffMax = 5 * time.Minute
)

// lifter is the panel's one lift. Its lock is held across a present and the backlight written after
// it, so a gain and its backlight always change together.
type lifter struct {
	mu sync.Mutex

	disp liftDisplay // the GPU path, nil where there is none
	on   bool        // the setting

	path    string // the backlight node and its range
	top     int
	asked   int     // the raw level asked for (SetBacklight); -1 before one is
	written int     // the raw level written
	gain    float64 // the gain the panel is drawn with

	// pos is where the ramp is, as a fraction of the asked level; the backlight follows it a raw level
	// at a time. stepped is when it last moved.
	pos     float64
	stepped time.Time

	// held is the brightest value to keep room for: the brightest of anything drawn in the last
	// liftHold (255 when not known). lowSince is when everything drawn started to be darker than held,
	// lowTop the brightest since.
	held     int
	lowSince time.Time
	lowTop   int

	canvas   bool      // the panel shows the canvas alone, which a ramp can draw again
	drawn    time.Time // the last present
	ramping  bool      // the ramp's goroutine is running
	disabled bool      // the helper cannot lift
	manual   bool      // no goroutine: a test steps the ramp itself

	// restUntil is when the lift may try a step again after the helper failed one (liftBackoff),
	// rest how long the last rest was; reset says the helper may still hold a gain the backlight is no
	// longer turned down for, to be put back to none with the next present. gone says the helper
	// itself is gone (not just refused), for the screen to leave the GPU (lost).
	restUntil time.Time
	rest      time.Duration
	reset     bool
	gone      bool

	// what was asked and written, integrated over time, for the report
	since              time.Time
	askedSum, wroteSum float64
	counted            time.Time
}

var lit = lifter{asked: -1, gain: 1, pos: 1, held: 255}

// liftDisplay is what the lift needs of the GPU path (gpu.Display).
type liftDisplay interface {
	SetLift(gain float64, redraw bool) error
}

// SetLift turns the lift on or off; a change shows over the next moments, as the ramp moves.
func (d *Device) SetLift(on bool) {
	lit.mu.Lock()
	defer lit.mu.Unlock()
	if on == lit.on {
		return
	}
	lit.on = on
	slog.Info("screen: adaptive backlight", "on", on, "gpu", lit.disp != nil)
	lit.wakeLocked(time.Now())
}

// lost is whether the lift found the GPU helper gone since it was last asked: the screen then goes back
// to the framebuffer at its next present.
func (l *lifter) lost() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	g := l.gone
	l.gone = false
	return g
}

// DrawnByGPU is whether the GPU helper draws the panel now (screen_gpu.go), so the lift can draw the
// pixels brighter: not before the screen is open, and not once the helper has failed.
func DrawnByGPU() bool {
	lit.mu.Lock()
	defer lit.mu.Unlock()
	return lit.disp != nil
}

// useLift gives the lift the GPU path (nil: none, the framebuffer's), putting it back to none.
func useLift(disp *gpu.Display) {
	if disp == nil {
		lit.use(nil)
		return
	}
	lit.use(disp)
}

func (l *lifter) use(disp liftDisplay) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if disp == nil {
		// The cores draw from now, without a gain: the backlight goes back to what was asked at once.
		l.gain, l.pos = 1, 1
		if l.asked >= 0 && l.written != l.asked {
			_ = l.writeLocked(l.asked, time.Now())
		}
	}
	l.disp = disp
	l.restUntil, l.rest, l.reset, l.gone = time.Time{}, 0, false, false
}

// setBacklight is SetBacklight's raw level, turned down by the lift's share.
func (l *lifter) setBacklight(path string, top, raw int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.path, l.top = path, top
	l.countLocked(now)
	l.asked = raw
	if raw == 0 && l.gain > 1 && l.disp != nil {
		// A dark panel: nothing to keep matched, so the lift is put back to none at once.
		if err := l.disp.SetLift(1, false); err == nil || errors.Is(err, gpu.ErrNotCanvas) {
			l.gain, l.pos = 1, 1
		}
	}
	out := raw
	if l.gain > 1 {
		// The gain stays: the level asked for moves slowly (relight), and rounding to a raw level
		// leaves the pair at most half a level apart, a third of a percent at the user's 25%.
		out = int(math.Round(float64(raw) * math.Pow(l.gain, -liftGamma)))
	}
	err := l.writeLocked(out, now)
	l.wakeLocked(now)
	return err
}

// asked level, in 0..BacklightMax, when known: what Backlight reads, rather than the lifted level.
func (l *lifter) askedLevel() (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.asked < 0 || l.top <= 0 || l.gain <= 1 {
		return 0, false
	}
	return (l.asked*BacklightMax + l.top/2) / l.top, true
}

func (l *lifter) writeLocked(raw int, now time.Time) error {
	if l.path == "" || raw < 0 {
		return nil
	}
	l.countLocked(now)
	if err := os.WriteFile(l.path, []byte(strconv.Itoa(raw)), 0o644); err != nil {
		return err
	}
	l.written = raw
	return nil
}

// present runs show (a present of the GPU path) with the lift: a ramp's next step is taken in it, its
// backlight written once it is on the panel, and what it measured kept. top is the brightest value
// show reports, or -1 for a frame it did not measure.
func (l *lifter) present(canvas bool, show func(measure bool) (top int, err error)) error {
	return l.presentAt(time.Now(), canvas, show)
}

func (l *lifter) presentAt(now time.Time, canvas bool, show func(measure bool) (top int, err error)) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	raw, gain, step := l.nextLocked(now)
	if l.reset && !step && l.disp != nil && !now.Before(l.restUntil) {
		// A failed step may have left the helper drawing with a gain: none, with this frame.
		if err := l.disp.SetLift(1, false); err == nil || errors.Is(err, gpu.ErrNotCanvas) {
			l.reset = false
		} else {
			l.failedLocked(err, now)
		}
	}
	if step {
		if err := l.disp.SetLift(gain, false); err != nil && !errors.Is(err, gpu.ErrNotCanvas) {
			step = false
			l.failedLocked(err, now)
		} else {
			l.rest = 0
		}
	}
	top, err := show(l.on && l.disp != nil && !l.disabled)
	if err != nil {
		return err
	}
	if step {
		l.gain = gain
		_ = l.writeLocked(raw, now)
	}
	l.canvas, l.drawn = canvas, now
	if top < 0 {
		top = 255
	}
	l.seenLocked(top, now)
	l.wakeLocked(now)
	return nil
}

// seenLocked keeps the brightest of what was drawn: at once when brighter than held, after liftHold
// of all of it being darker otherwise.
func (l *lifter) seenLocked(top int, now time.Time) {
	switch {
	case top >= l.held:
		l.held, l.lowSince = top, time.Time{}
	case l.lowSince.IsZero():
		l.lowSince, l.lowTop = now, top
	default:
		l.lowTop = max(l.lowTop, top)
	}
}

// targetLocked is the raw level the ramp is going to, as a fraction of the asked level, and whether
// the lift is in use at all.
func (l *lifter) targetLocked(now time.Time) float64 {
	if !l.lowSince.IsZero() && now.Sub(l.lowSince) >= liftHold {
		l.held, l.lowSince = l.lowTop, time.Time{}
	}
	if !l.on || l.disp == nil || l.disabled || now.Before(l.restUntil) || l.asked <= 0 || l.top <= 0 ||
		l.asked*1023 < liftMinRaw*l.top {
		return 1
	}
	return liftTarget(l.asked, l.held)
}

// liftTarget is the lowest raw level, as a fraction of asked, at which content no brighter than held
// still reaches its own light: asked * (held/255)^2.2, rounded up to a raw level, never under
// liftFloor.
func liftTarget(asked, held int) float64 {
	f := math.Max(math.Pow(float64(held)/255, liftGamma), liftFloor)
	raw := math.Ceil(float64(asked)*f - 1e-9)
	return math.Min(raw/float64(asked), 1)
}

// liftGain is the gain that matches a backlight at raw of asked: (asked/raw)^(1/2.2).
func liftGain(asked, raw int) float64 {
	if raw <= 0 || raw >= asked {
		return 1
	}
	return math.Pow(float64(asked)/float64(raw), 1/liftGamma)
}

// nextLocked moves the ramp on to now and says the raw level and gain of its next step, and whether
// there is one to take.
func (l *lifter) nextLocked(now time.Time) (raw int, gain float64, step bool) {
	target := l.targetLocked(now)
	dt := now.Sub(l.stepped).Seconds()
	if l.stepped.IsZero() || dt > 0.1 || dt < 0 {
		dt = liftTick.Seconds()
	}
	l.stepped = now
	switch {
	case l.pos > target:
		l.pos = math.Max(target, l.pos-liftDown*dt)
	case l.pos < target:
		l.pos = math.Min(target, l.pos+liftUp*dt)
	}
	if l.asked <= 0 || l.disp == nil || now.Before(l.restUntil) {
		return 0, 1, false
	}
	// Up a raw level as soon as the ramp passes it, so the gain never stays above what pos allows.
	raw = int(math.Ceil(float64(l.asked)*l.pos - 1e-9))
	raw = min(raw, l.asked)
	gain = liftGain(l.asked, raw)
	if math.Abs(gain-l.gain) < 1e-9 {
		return 0, 0, false
	}
	return raw, gain, true
}

// settledLocked is whether the ramp has nowhere to go, the panel is drawn with its gain, and no hold
// is running out.
func (l *lifter) settledLocked(now time.Time) bool {
	target := l.targetLocked(now)
	if l.pos != target || !l.lowSince.IsZero() {
		return false
	}
	if l.asked <= 0 || l.disp == nil || now.Before(l.restUntil) {
		return true // resting: the next present wakes the ramp again
	}
	raw := min(int(math.Ceil(float64(l.asked)*target-1e-9)), l.asked)
	return math.Abs(liftGain(l.asked, raw)-l.gain) < 1e-9
}

// wakeLocked starts the ramp's goroutine when there is a ramp to run and nothing running it.
func (l *lifter) wakeLocked(now time.Time) {
	if l.ramping || l.manual || l.settledLocked(now) {
		return
	}
	l.ramping = true
	go l.ramp()
}

// ramp steps the ramp while the panel is not being drawn, drawing the last canvas again with each
// step's gain. While frames are being drawn (a video's), they take the steps.
func (l *lifter) ramp() {
	for {
		l.mu.Lock()
		now := time.Now()
		if l.settledLocked(now) {
			l.ramping = false
			l.mu.Unlock()
			return
		}
		wait := liftTick
		switch {
		case !l.lowSince.IsZero() && l.pos == l.targetLocked(now):
			wait = max(liftHold-now.Sub(l.lowSince), liftTick) // nothing to do until the hold runs out
		case !l.canvas:
			// A video's frames take the steps; one that stopped on a frame keeps its pair as it is,
			// until the canvas is drawn again.
			wait = max(liftTick, min(time.Second, now.Sub(l.drawn)))
		case now.Sub(l.drawn) >= liftTick:
			l.redrawLocked(now)
		}
		l.mu.Unlock()
		time.Sleep(wait)
	}
}

// redrawLocked takes the ramp's next step on its own: the last canvas drawn again with its gain, then
// its backlight.
func (l *lifter) redrawLocked(now time.Time) {
	raw, gain, step := l.nextLocked(now)
	if !step {
		return
	}
	switch err := l.disp.SetLift(gain, true); {
	case err == nil:
		l.gain = gain
		l.rest = 0
		_ = l.writeLocked(raw, time.Now())
		l.drawn = time.Now()
	case errors.Is(err, gpu.ErrNotCanvas):
		l.canvas = false
	default:
		l.failedLocked(err, now)
	}
}

// failedLocked handles a step the helper failed. A helper that cannot lift turns the lift off for good;
// any other failure rests it (liftBackoff, doubling): the backlight goes back to what was asked at
// once, and no step is tried until the rest is over. A helper that is gone, not just refusing, is also
// the screen's to leave (lost, leaveGPU).
func (l *lifter) failedLocked(err error, now time.Time) {
	l.gain, l.pos = 1, 1
	_ = l.writeLocked(l.asked, now)
	if errors.Is(err, gpu.ErrNoLift) {
		slog.Warn("screen: the GPU helper cannot lift; the backlight stays as asked")
		l.disabled = true
		return
	}
	var st *gpu.StatusError
	if !errors.As(err, &st) {
		l.gone = true
	}
	l.rest = min(max(2*l.rest, liftBackoff), liftBackoffMax)
	l.restUntil = now.Add(l.rest)
	l.reset = true
	slog.Warn("screen: the GPU failed a lift step; the backlight stays as asked for a while", "err", err, "rest", l.rest)
}

// countLocked adds what was asked and written since the last count, and logs the saving once an
// hour while the lift is on.
func (l *lifter) countLocked(now time.Time) {
	if !l.counted.IsZero() && l.asked > 0 {
		dt := now.Sub(l.counted).Seconds()
		l.askedSum += float64(l.asked) * dt
		l.wroteSum += float64(l.written) * dt
	}
	l.counted = now
	if l.since.IsZero() {
		l.since = now
	}
	if now.Sub(l.since) >= liftReport {
		if l.on && l.askedSum > 0 {
			slog.Info("screen: adaptive backlight over the last hour",
				"saved_pct", math.Round(1000*(1-l.wroteSum/l.askedSum))/10,
				"asked_mean", math.Round(l.askedSum/now.Sub(l.since).Seconds()))
		}
		l.since, l.askedSum, l.wroteSum = now, 0, 0
	}
}
