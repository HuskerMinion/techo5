//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The power key is Screen power's off and on: a press puts a lit screen out and saves it so, the next
// lights it again at the brightness it had. A screen the night put out is lit by it too, and is no
// longer the night's.
func TestThePowerKeyTurnsTheScreenOffAndOn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := roomLightsDisplay()
	d.apply(true, 37, true)

	d.powerKey(time.Now())
	if d.on || d.power.Get() || config.Get().Screen.On {
		t.Fatalf("after a press: on %v, Screen power %v, saved %v; want all off", d.on, d.power.Get(), config.Get().Screen.On)
	}
	d.powerKey(time.Now())
	if !d.on || !d.power.Get() || !config.Get().Screen.On || d.ceiling != 37 {
		t.Fatalf("after a second press: on %v, Screen power %v, saved %v, brightness %d; want lit at 37",
			d.on, d.power.Get(), config.Get().Screen.On, d.ceiling)
	}

	d.mu.Lock()
	d.nightDark = true
	d.mu.Unlock()
	d.apply(false, 37, false)
	now := time.Now()
	d.powerKey(now)
	if !d.on || d.nightDark || !d.touchedAt.Equal(now) {
		t.Errorf("a press on a screen the night put out: on %v, night's %v, touched %v; want lit, not the night's, touched now",
			d.on, d.nightDark, d.touchedAt)
	}
}
