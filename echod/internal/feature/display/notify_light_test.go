//go:build !dot

package display

import (
	"math"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// A critical notification's light climbs from the backlight it found to its own over its rise, holds
// there while it rings, and goes back to what the screen would otherwise have over the same time once
// it stops; one with no rise comes up at once and still goes back over critBackMin.
func TestACriticalLightRisesAndComesBack(t *testing.T) {
	full := func(pct int) float64 { return float64(pct) * 10 } // 100% is 1000
	t0 := time.Unix(1_000_000, 0)
	l := notify.CriticalLight{Percent: 100, Rise: 10 * time.Second, Since: t0}
	var c critLit
	near := func(what string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 0.5 {
			t.Errorf("%s: %.1f, want %.1f", what, got, want)
		}
	}
	near("at the start", c.target(l, true, 200, 300, t0, full), 200)
	near("half way up", c.target(l, true, 999, 300, t0.Add(5*time.Second), full), 600)
	if !c.moving() {
		t.Error("not moving half way up")
	}
	near("up", c.target(l, true, 999, 300, t0.Add(10*time.Second), full), 1000)
	if c.moving() {
		t.Error("still moving once lit to the top")
	}
	stop := t0.Add(30 * time.Second)
	near("quieted", c.target(l, false, 1000, 300, stop, full), 1000)
	near("half way back", c.target(l, false, 650, 300, stop.Add(5*time.Second), full), 650)
	near("back", c.target(l, false, 300, 300, stop.Add(10*time.Second), full), 300)
	if c.moving() {
		t.Error("still moving once back")
	}
	near("after", c.target(l, false, 300, 250, stop.Add(20*time.Second), full), 250)

	// No rise: at once up, and back over critBackMin.
	c = critLit{}
	l = notify.CriticalLight{Percent: 50, Since: t0}
	near("at once", c.target(l, true, 100, 300, t0, full), 500)
	near("half way back, no rise", c.target(l, false, 500, 300, stop, full), 500)
	near("back, no rise", c.target(l, false, 400, 300, stop.Add(critBackMin/2), full), 400)
	near("back, no rise, done", c.target(l, false, 300, 300, stop.Add(critBackMin), full), 300)

	// A new ring starts over from the backlight it finds.
	l2 := notify.CriticalLight{Percent: 100, Rise: 10 * time.Second, Since: t0.Add(time.Hour)}
	near("a new ring", c.target(l2, true, 300, 300, l2.Since, full), 300)
}
