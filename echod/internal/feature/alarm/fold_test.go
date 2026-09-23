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
	// Ended and waited out: the bell runs on its own goroutine and reads the config, which the next
	// test swaps.
	t.Cleanup(func() {
		// a is this test's own, not the one ring.End reaches through Get, so it is stopped itself.
		a.Stop()
		ring.End()
		for end := time.Now().Add(2 * time.Second); ring.IsSounding() && time.Now().Before(end); {
			time.Sleep(time.Millisecond)
		}
	})

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
