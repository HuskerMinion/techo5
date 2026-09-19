package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMainlineIlluminanceUsesReportedScale(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sys/bus/iio/devices/iio:device12")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"name": "jsa1214\n", "in_illuminance_raw": "201\n", "in_illuminance_scale": "0.500000000\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := Reader{Root: root}
	path := r.LuxPath()
	if path != filepath.Join(dir, "in_illuminance_raw") {
		t.Fatalf("unexpected sensor %q", path)
	}
	if got := r.Lux(path); !got.Known || got.Value != 100.5 {
		t.Fatalf("scaled reading: %+v", got)
	}
	for _, scale := range []string{"invalid", "0", "-1", "NaN", "+Inf"} {
		if err := os.WriteFile(filepath.Join(dir, "in_illuminance_scale"), []byte(scale), 0600); err != nil {
			t.Fatal(err)
		}
		if got := r.Lux(path); got.Known {
			t.Errorf("scale %q reported %+v", scale, got)
		}
	}
	if err := os.Remove(filepath.Join(dir, "in_illuminance_scale")); err != nil {
		t.Fatal(err)
	}
	if got := r.Lux(path); got.Known {
		t.Fatalf("missing scale reported %+v", got)
	}
}
