package ring

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A ring started at a step of its own chimes at that step, held to the volume's range, and one started
// without (or at 0) at the ring volume set on the device.
func TestARingAtAStepOfItsOwnChimesAtIt(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, c := range []struct {
		step, want int
	}{
		{7, 7},
		{config.VolumeSteps + 5, config.VolumeSteps},
		{0, Level()},
		{-3, Level()},
	} {
		rm := quietBell(t, time.Minute)
		var e ends
		stop := StartAt("notification", "", nil, c.step, 0, 0, e.ended)
		waitFor(t, "a chime", func() bool { return rm.rounds() > 0 })
		stop()
		waitFor(t, "ended", func() bool { return e.count() > 0 })
		rm.mu.Lock()
		got := rm.steps[0]
		rm.mu.Unlock()
		if got != c.want {
			t.Errorf("step %d chimed at %d, want %d", c.step, got, c.want)
		}
		waitFor(t, "the bell to stop", func() bool {
			bell.mu.Lock()
			defer bell.mu.Unlock()
			return !bell.running && !IsSounding()
		})
	}
}

// A ring with a rise starts at the step it is given to start from and climbs evenly to its own; one that
// would start at or above its step rings at it from the first chime.
func TestARingAtAStepCanRiseToIt(t *testing.T) {
	r := &ringer{step: 20, stepFrom: 4, stepRise: 10 * time.Second, from: time.Unix(1000, 0)}
	for _, c := range []struct {
		after time.Duration
		want  int
	}{{0, 4}, {5 * time.Second, 12}, {10 * time.Second, 20}, {time.Minute, 20}, {-time.Second, 4}} {
		if got := r.stepFor(r.from.Add(c.after)); got != c.want {
			t.Errorf("after %v: step %d, want %d", c.after, got, c.want)
		}
	}
	r.stepFrom = 25
	if got := r.stepFor(r.from); got != 20 {
		t.Errorf("starting above its step: %d, want 20", got)
	}
}
