//go:build !dot

package ambient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The 2nd gen Show 8 on TECHO5's mainline kernel has no hwmsensor. Its AMS TCS3400 sits on the
// tcs3472 IIO driver (patched for the TCS3400), which hands out raw clear/red/green/blue counts and
// no lux. So echod polls the counts, sets the gain itself and works the lux out the way Amazon's
// own tcs3400 driver does.

// iioRoot is where IIO devices are listed; tests point it at a directory of their own.
var iioRoot = "/sys/bus/iio/devices"

// tcs3400Name is the IIO device name the patched driver gives a TCS3400.
const tcs3400Name = "tcs3400"

// tcs3400IntTime is the integration time asked for: 100 cycles of 2.78 ms. It fits inside the
// half-second poll, and at 64x one count is about 0.03 lux while at 1x the clear channel saturates
// near 80,000 lux.
const tcs3400IntTime = "0.278000"

// errStop is a reader that has stopped for good.
var errStop = errors.New("ambient: too many failed readings")

// findIIO is the directory of the IIO device with this name.
func findIIO(name string) (string, error) {
	names, _ := filepath.Glob(filepath.Join(iioRoot, "*", "name"))
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err == nil && strings.TrimSpace(string(b)) == name {
			return filepath.Dir(n), nil
		}
	}
	return "", fmt.Errorf("no IIO device named %s under %s", name, iioRoot)
}

// tcs3400 is the sensor's IIO device and the gain echod set on it.
type tcs3400 struct {
	dir    string
	gains  []int   // calibscale steps, low to high
	gain   int     // index into gains
	itime  float64 // integration time, seconds
	settle int     // readings to drop after a gain change
}

func openTCS3400() (*tcs3400, error) {
	dir, err := findIIO(tcs3400Name)
	if err != nil {
		return nil, err
	}
	t := &tcs3400{dir: dir}
	if err := t.write("in_intensity_integration_time", tcs3400IntTime); err != nil {
		return nil, err
	}
	if t.itime, err = t.float("in_intensity_integration_time"); err != nil || t.itime <= 0 {
		return nil, fmt.Errorf("ambient: reading the integration time back: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "calibscale_available"))
	if err != nil {
		return nil, fmt.Errorf("ambient: %w", err)
	}
	if t.gains = parseGains(string(b)); len(t.gains) == 0 {
		return nil, fmt.Errorf("ambient: no gains in %q", strings.TrimSpace(string(b)))
	}
	// Start in the middle; the first readings move it where the room needs it.
	if err := t.setGain(len(t.gains) / 2); err != nil {
		return nil, err
	}
	return t, nil
}

func parseGains(s string) []int {
	var gains []int
	for _, f := range strings.Fields(s) {
		if g, err := strconv.Atoi(f); err == nil && g > 0 {
			gains = append(gains, g)
		}
	}
	return gains
}

// close has nothing to let go of: every reading opens and closes its own files.
func (t *tcs3400) close() error { return nil }

func (t *tcs3400) write(attr, v string) error {
	if err := os.WriteFile(filepath.Join(t.dir, attr), []byte(v), 0o644); err != nil {
		return fmt.Errorf("ambient: setting %s: %w", attr, err)
	}
	return nil
}

func (t *tcs3400) float(attr string) (float64, error) {
	b, err := os.ReadFile(filepath.Join(t.dir, attr))
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
}

func (t *tcs3400) setGain(i int) error {
	if err := t.write("in_intensity_calibscale", strconv.Itoa(t.gains[i])); err != nil {
		return err
	}
	t.gain = i
	// The cycle running when the gain changed finishes on the old one, and the next poll may still
	// see it. One poll later the data is surely from the new gain.
	t.settle = 1
	return nil
}

// counts is one set of raw readings.
type counts struct{ c, r, g, b float64 }

func (t *tcs3400) read() (counts, error) {
	var v [4]float64
	for i, ch := range [4]string{"clear", "red", "green", "blue"} {
		n, err := t.float("in_intensity_" + ch + "_raw")
		if err != nil {
			return counts{}, err
		}
		v[i] = n
	}
	return counts{v[0], v[1], v[2], v[3]}, nil
}

// sample reads the counts and returns the lux. ok is false when the reading is not one to report:
// right after a gain change, or when the gain moved because the counts were out of range.
func (t *tcs3400) sample() (lux float64, ok bool, err error) {
	n, err := t.read()
	if err != nil {
		return 0, false, err
	}
	if t.settle > 0 {
		t.settle--
		return 0, false, nil
	}
	if next := nextGain(n.c, t.gains, t.gain, maxCount(t.itime)); next != t.gain {
		if err := t.setGain(next); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}
	top := t.gain == len(t.gains)-1
	return tcs3400Lux(n, float64(t.gains[t.gain]), t.itime, top), true, nil
}

// maxCount is the most a channel can hold at this integration time: 1024 counts per 2.78 ms cycle,
// up to the 16-bit register. (TCS3400 datasheet figures as the stock driver uses them: it computes
// the limit as cycles << 10, capped at 0xffff.)
func maxCount(itime float64) float64 {
	cycles := math.Round(itime / 0.00278)
	return math.Min(cycles*1024, 65535)
}

// nextGain is the gain step for the next reading. Above 80% of full scale (the stock driver's
// saturation line) the gain drops a step. It rises a step only when the clear count would still be
// under half that line at the higher gain, so it does not hunt between two steps.
func nextGain(clear float64, gains []int, cur int, max float64) int {
	high := 0.8 * max
	switch {
	case clear >= high && cur > 0:
		return cur - 1
	case cur < len(gains)-1 && clear*float64(gains[cur+1])/float64(gains[cur]) < high/2:
		return cur + 1
	}
	return cur
}

// Lux coefficients: the stock tcs3400 driver's built-in defaults (its "use defaults" path), read off
// the stock athena kernel: device/glass factor 446, red 0.090, green 1.000, blue -0.460 (the driver
// keeps them times 1000). This follows AMS's design note DN40 for the TCS3400 family. Athena's stock
// DT gives none of its own, so its real numbers may come from a per-unit calibration Amazon's
// userspace writes: these are not calibrated for the athena window.
const (
	luxDGF   = 446.0
	luxRCoef = 0.090
	luxGCoef = 1.000
	luxBCoef = -0.460
)

// tcs3400Lux converts raw counts to lux as the stock driver does:
//
//	IR    = (R + G + B - C + 1) / 2, not below zero (not taken off at the highest gain)
//	CPL   = integration time in ms * gain / DGF    (counts per lux)
//	lux   = (Rc*(R-IR) + Gc*(G-IR) + Bc*(B-IR)) / CPL
//
// A negative result (blue-heavy light at the low end) reads as 0.
func tcs3400Lux(n counts, gain, itime float64, topGain bool) float64 {
	ir := 0.0
	if !topGain {
		ir = math.Max(0, math.Floor((n.r+n.g+n.b-n.c+1)/2))
	}
	cpl := itime * 1000 * gain / luxDGF
	if cpl <= 0 {
		return 0
	}
	lux := (luxRCoef*(n.r-ir) + luxGCoef*(n.g-ir) + luxBCoef*(n.b-ir)) / cpl
	return math.Max(0, lux)
}

// startIIO opens the TCS3400 on athena's mainline kernel.
func (s *Sensor) startIIO() error {
	t, err := openTCS3400()
	if err != nil {
		return fmt.Errorf("ambient: %w", err)
	}
	s.mu.Lock()
	s.poll = t
	s.mu.Unlock()
	slog.Info("light sensor on", "device", t.dir, "period", period, "integration", t.itime,
		"gain", t.gains[t.gain])
	return nil
}

// runPoll polls the sensor every period. Every reading refreshes Current; Lux fires only when the
// rounded value changes, as the hwmsensor path only hears of changes.
func (s *Sensor) runPoll(ctx context.Context, t poller) error {
	tick := time.NewTicker(period)
	defer tick.Stop()
	failed := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		lux, ok, err := t.sample()
		if err != nil {
			// The driver answers EIO when a cycle is late; a run of them means it is gone.
			if failed++; failed >= 10 {
				return fmt.Errorf("%w: %v", errStop, err)
			}
			slog.Debug("light sensor reading failed", "err", err)
			continue
		}
		failed = 0
		if ok {
			s.record(math.Round(lux))
		}
	}
}

// record keeps a reading and tells the listeners if it changed.
func (s *Sensor) record(lux float64) {
	s.mu.Lock()
	changed := s.at.IsZero() || lux != s.last
	s.last, s.at = lux, time.Now()
	s.mu.Unlock()
	if changed {
		s.Lux.Emit(lux)
	}
}
