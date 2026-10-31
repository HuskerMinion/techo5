//go:build !dot && !spot

package display

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A dark room, at 0, 1 or 2 lux, gets exactly the dimmest; brighter rooms rise on the log curve to the
// whole ceiling at brightLux and stay there (#78).
func TestAllowed(t *testing.T) {
	for _, c := range []struct {
		lux, dark, want float64
	}{
		{0, 0.12, 0.12},
		{1, 0.12, 0.12},
		{1, 0.03, 0.03},
		{2, 0.12, 0.12},
		{brightLux, 0.12, 1},
		{10 * brightLux, 0.03, 1},
		{-5, 0.12, 0.12},
	} {
		if got := allowed(c.lux, c.dark); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("allowed(%v, %v) = %v, want %v", c.lux, c.dark, got, c.want)
		}
	}
	// Between, it climbs with the room and stays under the ceiling.
	prev := allowed(2, 0.05)
	for _, lux := range []float64{3, 5, 20, 100, 399} {
		got := allowed(lux, 0.05)
		if got <= prev || got >= 1 {
			t.Errorf("allowed(%v) = %v after %v", lux, got, prev)
		}
		prev = got
	}
}

// The screen's steps: one at a time, held at the ends, and from a value Home Assistant set between two
// steps, up is the next one and down the one before.
func TestNextDimmest(t *testing.T) {
	for _, c := range []struct{ cur, by, want int }{
		{12, +1, 15},
		{12, -1, 10},
		{1, -1, 1},
		{50, +1, 50},
		{7, +1, 8},
		{7, -1, 6},
		{45, +1, 50},
		{45, -1, 40},
	} {
		if got := nextDimmest(c.cur, c.by); got != c.want {
			t.Errorf("nextDimmest(%d, %+d) = %d, want %d", c.cur, c.by, got, c.want)
		}
	}
}

// The Brightness row steps by 5 at the bottom, so a bedroom panel can go down to a 5% ceiling, and
// lands on a step from a value Home Assistant set between two.
func TestNextBrightness(t *testing.T) {
	for _, c := range []struct{ cur, by, want int }{
		{25, -1, 20},
		{10, -1, 5},
		{5, -1, 5},
		{100, +1, 100},
		{60, +1, 70},
		{33, +1, 40},
		{33, -1, 30},
		{3, -1, 3},
	} {
		if got := nextStep(brightnessSteps[:], c.cur, c.by); got != c.want {
			t.Errorf("nextStep(brightness, %d, %+d) = %d, want %d", c.cur, c.by, got, c.want)
		}
	}
}

// The backlight a dark bedroom gets, as on a Show 8 at 25% Brightness reading 1 lux: about 8 of 255 at
// the default dimmest, where the old curve gave about 14, and the floor of 4 at the lowest setting.
// Without auto, or without a reading, it is the Brightness setting as it was.
func TestDayBacklight(t *testing.T) {
	for _, c := range []struct {
		ceiling int
		auto    bool
		lux     float64
		dark    float64
		want    float64
	}{
		{25, true, 1, 0.12, 7.65},
		{25, true, 0, 0.12, 7.65},
		{25, true, 1, 0.01, floor},
		{60, true, 1, 0.12, 18.36},
		{60, true, 1, 0.03, floor + 0.59},
		{100, true, 1000, 0.12, 255},
		{44, false, 1, 0.12, 112.2},
		{1, false, 0, 0.12, floor},
	} {
		if got := dayBacklight(c.ceiling, c.auto, c.lux, c.dark); math.Abs(got-c.want) > 0.01 {
			t.Errorf("dayBacklight(%d%%, auto %v, %v lux, dimmest %v) = %.2f, want %.2f", c.ceiling, c.auto, c.lux, c.dark, got, c.want)
		}
	}
	// The old curve, for the record of what changed: 1 lux at 25% was about 14.
	if old := 63.75 * (0.12 + 0.88*math.Log10(2)/math.Log10(401)); math.Round(old) != 14 {
		t.Errorf("old curve at 1 lux, 25%%: %.2f", old)
	}
}

// The light before an alarm rises with the clock, not the room: the settle ticker follows it while it
// runs, and puts the screen back to its own level once it is over.
func TestSettleDrivesTheSunrise(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	was := sunriseProgress
	t.Cleanup(func() { sunriseProgress = was })

	progress := 0.2
	sunriseProgress = func(time.Time) float64 { return progress }
	d := &Display{on: true, ceiling: 100}
	d.relight(true)

	level := func() float64 {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.level
	}
	start := level()
	progress = 0.8
	d.settleStep()
	if risen := level(); risen <= start {
		t.Fatalf("the sunrise did not rise: %.1f at 20%%, %.1f at 80%%", start, risen)
	}

	progress = 0
	d.settleStep()
	if got, want := level(), dayBacklight(100, false, 0, dimmest()); got != want {
		t.Fatalf("after the sunrise the backlight is %.1f, want the screen's own %.1f", got, want)
	}
}
