//go:build !dot

package ambient

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// The lux is the driver's raw count times its scale, and a sensor that cannot be read says so.
func TestRawLuxIsCountsTimesScale(t *testing.T) {
	dir := t.TempDir()
	r := rawLux{dir: dir}
	if _, ok, err := r.sample(); err == nil || ok {
		t.Errorf("an empty directory read as a sensor: ok=%v err=%v", ok, err)
	}
	for name, v := range map[string]string{"in_illuminance_raw": "94\n", "in_illuminance_scale": "0.024000\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lux, ok, err := r.sample()
	if err != nil || !ok || math.Abs(lux-2.256) > 1e-9 {
		t.Errorf("sample() = %v, %v, %v; want 2.256", lux, ok, err)
	}
}
