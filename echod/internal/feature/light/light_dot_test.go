//go:build dot

package light

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// A ring saved off still lends its color: the wake word's effect and the room's take it from Base,
// and a blue ring turned off must not wake in the white the light starts in.
func TestARingSavedOffKeepsItsColor(t *testing.T) {
	l := build()
	followed := 0
	l.OnColor(func() { followed++ })

	var c config.Config
	c.Ring.Light = config.Light{On: false, Brightness: 0.5, Blue: 1, Effect: "no such effect"}
	l.Restore(c)

	if s := l.light.Get(); s.On || s.Effect != "" {
		t.Errorf("restored on %v, effect %q; want off with no effect", s.On, s.Effect)
	}
	if got, want := l.Base(), (led.Color{B: 128}); got != want {
		t.Errorf("base %v, want %v", got, want)
	}
	if followed != 1 {
		t.Errorf("followers told %d times, want once", followed)
	}
}

// A ring saved off with nothing to light by takes white at full, as a bare "on" would.
func TestARingSavedOffBlackLendsWhite(t *testing.T) {
	l := build()
	var c config.Config
	c.Ring.Light = config.Light{}
	l.Restore(c)

	if got, want := l.Base(), (led.Color{R: 255, G: 255, B: 255}); got != want {
		t.Errorf("base %v, want %v", got, want)
	}
}

// A ring saved on comes back on, in its color.
func TestARingSavedOnComesBackOn(t *testing.T) {
	l := build()
	var c config.Config
	c.Ring.Light = config.Light{On: true, Brightness: 1, Red: 1}
	l.Restore(c)

	if s := l.light.Get(); !s.On {
		t.Error("restored off")
	}
	if got, want := l.Base(), (led.Color{R: 255}); got != want {
		t.Errorf("base %v, want %v", got, want)
	}
}
