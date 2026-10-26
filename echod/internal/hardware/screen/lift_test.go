//go:build !dot && !panel

package screen

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/gpu"
)

// The pair a step leaves is exact: content no brighter than held, drawn with the gain, reaches 255 at
// most, and the gain cancels the backlight it was chosen for.
func TestLiftTargetNeverClips(t *testing.T) {
	for asked := liftMinRaw; asked <= 1023; asked++ {
		for held := 0; held <= 255; held++ {
			f := liftTarget(asked, held)
			raw := int(math.Ceil(float64(asked)*f - 1e-9))
			g := liftGain(asked, raw)
			if float64(held)*g > 255+1e-9 {
				t.Fatalf("asked %d held %d: raw %d gain %.5f lifts %d to %.3f", asked, held, raw, g, held, float64(held)*g)
			}
			if f < liftFloor-1e-9 || f > 1 {
				t.Fatalf("asked %d held %d: target %.4f out of range", asked, held, f)
			}
			// what the panel shows: backlight raw times (code times gain)^2.2, against asked times code^2.2
			if raw > 0 && math.Abs(float64(raw)*math.Pow(g, liftGamma)/float64(asked)-1) > 1e-9 {
				t.Fatalf("asked %d raw %d: gain %.5f does not cancel the backlight", asked, raw, g)
			}
		}
	}
	// The user's Show 8: 25% brightness is raw 156; the theme's text at 233 leaves 18%.
	if got := liftTarget(156, 233); math.Abs(got-128.0/156) > 1e-9 {
		t.Errorf("156 at 233: %.4f, want 128/156", got)
	}
}

type fakeLift struct {
	gain    float64
	redraws int
	refuse  error
	calls   int
}

func (f *fakeLift) SetLift(gain float64, redraw bool) error {
	f.calls++
	if f.refuse != nil {
		return f.refuse
	}
	f.gain = gain
	if redraw {
		f.redraws++
	}
	return nil
}

func newTestLifter(t *testing.T, asked int) (*lifter, *fakeLift, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brightness")
	f := &fakeLift{gain: 1}
	l := &lifter{asked: -1, gain: 1, pos: 1, held: 255, manual: true, disp: f, on: true}
	if err := l.setBacklight(path, 1023, asked); err != nil {
		t.Fatal(err)
	}
	return l, f, path
}

func readRaw(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// shown is a frame of content whose brightest value is top, at now.
func shown(t *testing.T, l *lifter, f *fakeLift, now time.Time, top int) (clipped bool) {
	t.Helper()
	if err := l.presentAt(now, true, func(bool) (int, error) {
		clipped = float64(top)*f.gain > 255+1e-9
		return top, nil
	}); err != nil {
		t.Fatal(err)
	}
	return clipped
}

// checkPair is the panel's backlight against the gain it is drawn with: they must cancel, to the
// rounding of one raw level.
func checkPair(t *testing.T, l *lifter, f *fakeLift, path string, when string) {
	t.Helper()
	raw := readRaw(t, path)
	if f.gain != l.gain {
		t.Fatalf("%s: drawn with %.5f, lift says %.5f", when, f.gain, l.gain)
	}
	if want := liftGain(l.asked, raw); math.Abs(want-f.gain) > 1e-9 {
		t.Fatalf("%s: backlight %d of %d wants gain %.5f, drawn with %.5f", when, raw, l.asked, want, f.gain)
	}
}

// The clock: dark content held 10 s, then the backlight goes down a raw level at a time over a few
// seconds; a sheet with brighter text brings it back up within a fifth of a second, its highlights
// held at 255 only for those frames; nothing already shown ever clips.
func TestLiftRampsAndHolds(t *testing.T) {
	l, f, path := newTestLifter(t, 156)
	t0 := time.Unix(1e6, 0)
	now := t0
	tick := 16 * time.Millisecond
	idle := func(until time.Time) {
		for ; now.Before(until); now = now.Add(liftTick) {
			l.mu.Lock()
			l.redrawLocked(now)
			l.mu.Unlock()
			checkPair(t, l, f, path, now.Sub(t0).String())
		}
	}
	if shown(t, l, f, now, 233) {
		t.Fatal("the first frame clipped")
	}
	idle(t0.Add(liftHold - time.Second))
	if got := readRaw(t, path); got != 156 {
		t.Fatalf("within the hold: backlight %d, want 156", got)
	}
	last := 156
	start := now
	for ; now.Before(t0.Add(30 * time.Second)); now = now.Add(liftTick) {
		l.mu.Lock()
		l.redrawLocked(now)
		l.mu.Unlock()
		raw := readRaw(t, path)
		if last-raw > 1 {
			t.Fatalf("at %v: down %d raw levels in one step", now.Sub(t0), last-raw)
		}
		if raw == 128 && last != 128 {
			took := now.Sub(start)
			t.Logf("down from 156 to 128 in %v", took)
			if took < 3*time.Second || took > 8*time.Second {
				t.Errorf("down to 128 took %v", took)
			}
		}
		last = raw
		checkPair(t, l, f, path, now.Sub(t0).String())
	}
	if last != 128 || math.Abs(233*f.gain-255) > 0.1 {
		t.Fatalf("settled at %d, gain %.4f (233 drawn at %.2f)", last, f.gain, 233*f.gain)
	}
	if !l.settledLocked(now) {
		t.Fatal("not settled")
	}
	// A sheet comes up with text at 250.
	clips := 0
	up := now
	for i := 0; i < 60; i++ {
		if shown(t, l, f, now, 250) {
			clips++
		}
		checkPair(t, l, f, path, "sheet")
		now = now.Add(tick)
	}
	t.Logf("the sheet's highlights held at 255 for %d frames; backlight back to %d", clips, readRaw(t, path))
	if clips == 0 || time.Duration(clips)*tick > 250*time.Millisecond {
		t.Errorf("the sheet's highlights were held at 255 for %d frames", clips)
	}
	if 250*f.gain > 255+1e-9 {
		t.Errorf("after %v the sheet still clips", now.Sub(up))
	}
	// Back to the clock: the sheet's room is kept for the hold, then given back slowly.
	shown(t, l, f, now, 233)
	at := readRaw(t, path)
	idle(now.Add(liftHold - time.Second))
	if got := readRaw(t, path); got != at {
		t.Errorf("within the hold after the sheet: %d, want %d", got, at)
	}
}

// Turned off, or the asked level dropping under the lift's floor (the night), it goes back to none.
func TestLiftOffAndDark(t *testing.T) {
	l, f, path := newTestLifter(t, 156)
	now := time.Unix(1e6, 0)
	shown(t, l, f, now, 0) // all black
	for end := now.Add(30 * time.Second); now.Before(end); now = now.Add(liftTick) {
		l.mu.Lock()
		l.redrawLocked(now)
		l.mu.Unlock()
	}
	if got, want := readRaw(t, path), int(math.Ceil(156*liftFloor)); got != want {
		t.Fatalf("black screen: %d, want the floor %d", got, want)
	}
	l.mu.Lock()
	l.on = false
	l.mu.Unlock()
	for end := now.Add(2 * time.Second); now.Before(end); now = now.Add(liftTick) {
		l.mu.Lock()
		l.redrawLocked(now)
		l.mu.Unlock()
		checkPair(t, l, f, path, "off")
	}
	if got := readRaw(t, path); got != 156 || f.gain != 1 {
		t.Fatalf("off: %d at gain %.4f", got, f.gain)
	}
	// Night: asked under the lift's floor stays as asked.
	l.mu.Lock()
	l.on = true
	l.mu.Unlock()
	if err := l.setBacklight(path, 1023, 12); err != nil {
		t.Fatal(err)
	}
	for end := now.Add(30 * time.Second); now.Before(end); now = now.Add(liftTick) {
		shown(t, l, f, now, 0)
	}
	if got := readRaw(t, path); got != 12 || f.gain != 1 {
		t.Fatalf("night: %d at gain %.4f", got, f.gain)
	}
	// Off altogether: the gain goes at once, there being nothing to see.
	if err := l.setBacklight(path, 1023, 0); err != nil {
		t.Fatal(err)
	}
	if got := readRaw(t, path); got != 0 {
		t.Fatalf("dark: %d", got)
	}
}

// A helper from before the lift: the backlight stays as asked, for good.
func TestLiftOldHelper(t *testing.T) {
	l, f, path := newTestLifter(t, 156)
	f.refuse = gpu.ErrNoLift
	now := time.Unix(1e6, 0)
	for end := now.Add(30 * time.Second); now.Before(end); now = now.Add(liftTick) {
		shown(t, l, f, now, 200)
	}
	if got := readRaw(t, path); got != 156 || !l.disabled {
		t.Fatalf("old helper: %d, disabled %v", got, l.disabled)
	}
}

// A video's frames are not measured: the lift goes back to none under them, each frame taking a step.
func TestLiftVideo(t *testing.T) {
	l, f, path := newTestLifter(t, 500)
	now := time.Unix(1e6, 0)
	shown(t, l, f, now, 233)
	for end := now.Add(30 * time.Second); now.Before(end); now = now.Add(liftTick) {
		l.mu.Lock()
		l.redrawLocked(now)
		l.mu.Unlock()
	}
	if readRaw(t, path) >= 500 {
		t.Fatal("never lifted")
	}
	for i := 0; i < 20; i++ {
		if err := l.presentAt(now, false, func(bool) (int, error) { return -1, nil }); err != nil {
			t.Fatal(err)
		}
		checkPair(t, l, f, path, "video")
		now = now.Add(33 * time.Millisecond)
	}
	if got := readRaw(t, path); got != 500 || f.gain != 1 {
		t.Fatalf("under video: %d at gain %.4f", got, f.gain)
	}
}

// A helper that fails a step is not asked again at every tick: the lift rests, the backlight back to
// what was asked at once, for liftBackoff and then twice as long after each failure in a row, and
// takes its steps again once the helper answers. A refusal leaves the GPU to the screen; a helper that
// is gone is reported (lost) for the screen to leave it.
func TestLiftRestsAfterAFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		gone bool
	}{
		{"refused", &gpu.StatusError{Errno: syscall.EIO}, false},
		{"gone", errors.New("gpu: short reply (0 bytes): the helper went away"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, f, path := newTestLifter(t, 156)
			now := time.Unix(1e6, 0)
			shown(t, l, f, now, 200)
			for end := now.Add(liftHold + 3*time.Second); now.Before(end); now = now.Add(liftTick) {
				l.mu.Lock()
				l.redrawLocked(now)
				l.mu.Unlock()
			}
			if readRaw(t, path) >= 156 {
				t.Fatal("the lift took no step")
			}
			f.refuse = tc.err
			f.calls = 0
			start := now
			for end := now.Add(liftBackoff - time.Second); now.Before(end); now = now.Add(liftTick) {
				l.mu.Lock()
				l.redrawLocked(now)
				l.mu.Unlock()
				shown(t, l, f, now, 200)
			}
			if f.calls != 1 {
				t.Errorf("the helper was asked %d times in %v after failing, want once", f.calls, now.Sub(start))
			}
			if got := readRaw(t, path); got != 156 || l.gain != 1 {
				t.Errorf("resting: backlight %d at gain %.3f, want 156 at none", got, l.gain)
			}
			if !l.settledLocked(now) {
				t.Error("the ramp keeps running while it rests")
			}
			if got := l.lost(); got != tc.gone {
				t.Errorf("lost: %v, want %v", got, tc.gone)
			}
			if l.lost() {
				t.Error("lost twice for one failure")
			}
			// Still failing when the rest is over: it rests twice as long.
			now = l.restUntil.Add(time.Millisecond)
			shown(t, l, f, now, 200)
			if l.rest != 2*liftBackoff {
				t.Errorf("second rest %v, want %v", l.rest, 2*liftBackoff)
			}
			// The helper answers again: the gain is put back to none, then the steps resume.
			f.refuse = nil
			now = l.restUntil.Add(time.Millisecond)
			shown(t, l, f, now, 200)
			if l.reset || f.gain != 1 {
				t.Errorf("after the rest: reset %v, drawn at %.3f", l.reset, f.gain)
			}
			for end := now.Add(liftHold + 3*time.Second); now.Before(end); now = now.Add(liftTick) {
				l.mu.Lock()
				l.redrawLocked(now)
				l.mu.Unlock()
			}
			if readRaw(t, path) >= 156 {
				t.Error("no step after the helper answered again")
			}
			checkPair(t, l, f, path, "after")
		})
	}
}

// A slow helper does not hold the lift's lock for longer than its own bound: SetLift is bounded by
// the gpu package (liftWait), and one failure rests the lift, so a ramp running into a helper that
// takes its time makes one slow call, not one a tick.
func TestLiftSlowHelperOnce(t *testing.T) {
	l, f, _ := newTestLifter(t, 156)
	now := time.Unix(1e6, 0)
	shown(t, l, f, now, 200)
	f.refuse = errors.New("gpu: no reply in time")
	for end := now.Add(liftHold + 5*time.Second); now.Before(end); now = now.Add(liftTick) {
		l.mu.Lock()
		l.redrawLocked(now)
		l.mu.Unlock()
	}
	if f.calls != 1 {
		t.Errorf("%d calls to a silent helper over the hold and 5 s, want 1", f.calls)
	}
}
