//go:build !dot

package ambient

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The Lenovo ThinkSmart View has a Vishay VCNL4200 light and proximity sensor, which the kernel's
// vcnl4000 driver serves as an IIO device of that name: in_illuminance_raw counts, and
// in_illuminance_scale lux a count (0.024 on a unit read 2026-11-19). It is polled, as the TCS3400
// is.
const vcnl4200Name = "vcnl4000"

// rawLux is an IIO light sensor read as raw counts times its scale.
type rawLux struct{ dir string }

func (r rawLux) sample() (float64, bool, error) {
	raw, err := readFloat(filepath.Join(r.dir, "in_illuminance_raw"))
	if err != nil {
		return 0, false, err
	}
	scale, err := readFloat(filepath.Join(r.dir, "in_illuminance_scale"))
	if err != nil {
		return 0, false, err
	}
	return raw * scale, true, nil
}

func (rawLux) close() error { return nil }

func readFloat(path string) (float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
}

func (s *Sensor) startVCNL4200() error {
	dir, err := findIIO(vcnl4200Name)
	if err != nil {
		return fmt.Errorf("ambient: %w", err)
	}
	r := rawLux{dir: dir}
	lux, _, err := r.sample()
	if err != nil {
		return fmt.Errorf("ambient: %w", err)
	}
	s.mu.Lock()
	s.poll = r
	s.mu.Unlock()
	slog.Info("light sensor on", "device", dir, "period", period, "lux", lux)
	return nil
}
