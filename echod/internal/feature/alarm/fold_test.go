package alarm

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
)

// A second alarm going off into one somebody has silenced is a new reason to ring: it must be heard,
// not folded into the silence that was asked of the first.
func TestAnAlarmIntoASilencedRingIsHeard(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := build()
	now := time.Now()
	a.fire(source{key: "first"}, now)
	defer ring.End()

	if !ring.Silence() || !ring.Offered() {
		t.Fatal("the first alarm could not be silenced")
	}
	a.fire(source{key: "second"}, now)
	if ring.Offered() {
		t.Error("the second alarm left the ring silenced and waiting")
	}
	if !a.Ringing() {
		t.Error("nothing is ringing after the second alarm")
	}
}
