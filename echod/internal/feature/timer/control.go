package timer

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// Pausing, resuming and changing how long a timer of the device's own has left, from Home Assistant.
//
// Home Assistant's own timers, set by voice through Assist, are refused here as they are by cancel: Home
// Assistant keeps them, and changes them when asked by voice, and a change made here would be undone by
// its next event.

// controlActions are the actions that change a timer that is already running.
func (t *Timers) controlActions() []*esphome.Action {
	id := esphome.Arg{Name: "id", Type: esphome.ArgString}
	by := esphome.Arg{Name: "duration", Type: esphome.ArgString}
	action := func(name string, args ...esphome.Arg) *esphome.Action {
		return &esphome.Action{
			Name: name,
			Args: args,
			Run: func(c esphome.Call) (any, error) {
				return nil, t.control(name, c.String("id"), c.String("duration"))
			},
		}
	}
	return []*esphome.Action{
		action("timer_pause", id),
		action("timer_resume", id),
		action("timer_add", id, by),
		action("timer_remove", id, by),
	}
}

// control is what each of those actions does with the arguments it was given.
func (t *Timers) control(action, id, duration string) error {
	id = strings.TrimSpace(id)
	switch action {
	case "timer_pause":
		return t.eachFromHA(id, t.Pause)
	case "timer_resume":
		return t.eachFromHA(id, t.Resume)
	}
	d, err := ParseDuration(duration)
	if err != nil {
		return err
	}
	if action == "timer_remove" {
		d = -d
	}
	return t.adjustFromHA(id, d)
}

// eachFromHA runs do on one timer, or on every timer of the device's own for "all". A timer that ends
// between being listed and being reached, or has just run out, is passed over rather than stopping the
// rest: "all" means all of them that are still there.
func (t *Timers) eachFromHA(id string, do func(string) error) error {
	if id != "all" {
		return do(id)
	}
	n, skipped := 0, 0
	for _, c := range t.List(time.Now()) {
		if !c.Local {
			continue
		}
		switch err := do(c.ID); {
		case err == nil:
			n++
		case errors.Is(err, errNoTimer), errors.Is(err, errRunOut):
			skipped++
		default:
			return err
		}
	}
	slog.Info("timers changed from home assistant", "count", n, "passed_over", skipped)
	return nil
}

// adjustFromHA is Adjust for the actions, which take one timer: adding the same minutes to every
// timer is not what anybody means.
func (t *Timers) adjustFromHA(id string, d time.Duration) error {
	if id == "all" {
		return errors.New("timers: adding and taking off time takes one timer's id, not all")
	}
	return t.Adjust(id, d)
}

var (
	// errNoTimer is an id that is not held here, or no longer.
	errNoTimer = errors.New("timers: no such timer on this device")

	// errRunOut is a timer that has run out and is about to ring. It is left to ring: changing it now
	// would race the countdown that is ending it.
	errRunOut = errors.New("timers: that timer has already run out")

	// errTooShort is a change that would leave a timer with no time at all.
	errTooShort = errors.New("timers: that is more than the timer has left; cancel it instead")

	// errTooLong is a change that would take a timer past the longest one may be.
	errTooLong = fmt.Errorf("timers: a timer can be at most %v", Longest)
)

// Pause stops one of the device's own timers where it is. A paused timer stays paused.
func (t *Timers) Pause(id string) error {
	return t.change(id, "paused", func(c *timer, now time.Time) error {
		if c.active {
			c.left, c.at, c.active = c.remaining(now), now, false
		}
		return nil
	})
}

// Resume sets a paused timer of the device's own running again. A running timer goes on running.
func (t *Timers) Resume(id string) error {
	return t.change(id, "resumed", func(c *timer, now time.Time) error {
		if !c.active {
			c.at, c.active = now, true
		}
		return nil
	})
}

// Adjust gives one of the device's own timers more time, or less for a negative d, running or paused. It
// refuses to leave a timer with nothing: running out is the timer's to do, not a change's.
func (t *Timers) Adjust(id string, d time.Duration) error {
	return t.change(id, "changed", func(c *timer, now time.Time) error {
		left := c.remaining(now) + d
		switch {
		case left <= 0:
			return errTooShort
		case left > Longest:
			return errTooLong
		}
		c.left, c.at = left, now
		// The total is what the countdown is drawn against, so it grows with an added minute, and never
		// ends up shorter than what is left.
		c.total = max(c.total+d, left)
		return nil
	})
}

// change applies f to one of the device's own timers, then saves, wakes the countdown and tells the
// screen and Home Assistant, as a new timer does.
//
// A running timer that has reached zero is refused whatever the change. The countdown has picked it out
// to ring, or is about to, and paused at zero it would never ring, while time added after it was picked
// out would ring it with time still left.
func (t *Timers) change(id, what string, f func(c *timer, now time.Time) error) error {
	if id == "" {
		return errors.New("timers: which timer? give its id, from timer_start or alarms_list")
	}
	if !strings.HasPrefix(id, localPrefix) {
		return fmt.Errorf("timers: %q was set through Home Assistant; change it there", id)
	}
	t.mu.Lock()
	now := time.Now()
	c, ok := t.held[id]
	if !ok {
		t.mu.Unlock()
		return fmt.Errorf("%w: %q", errNoTimer, id)
	}
	if c.active && c.remaining(now) <= 0 {
		t.mu.Unlock()
		return errRunOut
	}
	before := c.remaining(now)
	if err := f(c, now); err != nil {
		t.mu.Unlock()
		return err
	}
	name, left, active := c.name, c.remaining(now), c.active
	by := left - before
	saved := t.saved(now)
	t.mu.Unlock()

	saveLocal(saved)
	slog.Info("timer "+what+" from home assistant", "id", id, "name", name, "left", left, "running", active)
	t.listFollow(id, what, by)

	select {
	case t.woke <- struct{}{}:
	default:
	}
	t.show()
	t.publish()
	t.Changed.Emit(struct{}{})
	return nil
}
