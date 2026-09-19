//go:build !dot

package ambient

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIIOReportsScaledLuxAndCancels(t *testing.T) {
	dir := t.TempDir()
	for name, value := range map[string]string{"in_illuminance_raw": "90", "in_illuminance_scale": "0.5"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := &Sensor{luxPath: filepath.Join(dir, "in_illuminance_raw")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got float64
	s.Lux.Listen(func(lux float64) { got = lux; cancel() })
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got != 45 {
		t.Fatalf("got %v lux", got)
	}
	if value, at, ok := s.Current(); !ok || at.IsZero() || value != got {
		t.Fatalf("current reading: %v %v", value, ok)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
