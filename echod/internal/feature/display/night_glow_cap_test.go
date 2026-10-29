//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The night light turns a bright screen down to its glow, and leaves a screen already darker than
// the glow where it is (#335).
func TestTheNightLightNeverTurnsTheScreenUp(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	d := &Display{on: true, ceiling: 100, nightGlow: true}
	d.relight(true)
	if want := float64(d.glowBacklight()); d.level != want {
		t.Fatalf("a bright screen at night: %.1f, want the glow's %.1f", d.level, want)
	}

	d.ceiling = 1
	d.relight(true)
	day := dayBacklight(1, false, 0, dimmest())
	if day >= float64(d.glowBacklight()) {
		t.Fatalf("test needs a day level under the glow: day %.1f, glow %d", day, d.glowBacklight())
	}
	if d.level != day {
		t.Fatalf("a dark screen at night: %.1f, want it left at %.1f", d.level, day)
	}
}
