// Package buttons owns the four buttons on top of the device.
//
// It reads the input nodes and says what happened. It does not know what any of it means: that a long
// press of the action button reaches the second assistant belongs to whatever is listening, and
// keeping the two apart is what lets the buttons work when nothing else does.
package buttons

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	// Early: the buttons should work whatever else is wrong, so they must not be downstream of a
	// network listener or lost to one read error.
	component.Register(component.Hardware, Get(), component.Order(10),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

// Name is which button. The evdev codes are the device's own.
type Name string

const (
	Mute       Name = "mute"
	VolumeDown Name = "volume_down"
	VolumeUp   Name = "volume_up"
	Action     Name = "action"
)

// LongPress is how long a button must be held to count as held rather than pressed, and
// RepeatInterval how fast a repeating button ramps while it is down.
const (
	LongPress      = 700 * time.Millisecond
	RepeatInterval = 200 * time.Millisecond
)

// Kind is what happened to a button.
type Kind string

const (
	// Tap is a short press. A repeating button reports it the moment it goes down, so a tap moves one
	// step; anything else reports it on release, once its length is known.
	Tap Kind = "tap"

	// Hold is the button still being down after LongPress. It is reported once, as it happens rather
	// than on release: waiting for release to act on a hold feels broken.
	Hold Kind = "hold"

	// Repeat is a repeating button still being down, every RepeatInterval.
	Repeat Kind = "repeat"

	// LongHold is a button that does not repeat still being down after longHoldAfter, reported once,
	// as it happens. It comes after that press's Hold, so whatever the hold started is still going
	// and is the listener's to undo. Devices where longHoldAfter is zero never report one.
	LongHold Kind = "long_hold"

	// DoubleTap is a second tap within doubleTap of the first, and comes after both of their Taps
	// rather than instead of them. The first tap has already acted by then - the action button starts
	// listening the moment it is let go, because a talk button that waits to see whether a second tap
	// is coming feels broken - so a listener for this undoes what the first one started, the way the
	// conversation already undoes the turn a hold began on its way to something longer.
	DoubleTap Kind = "double_tap"
)

// doubleTap is how close the second tap has to be. Long enough for a deliberate double tap on a
// button people press with one finger, short enough that two separate presses are not mistaken for
// one gesture.
const doubleTap = 400 * time.Millisecond

// LongHolds reports whether this device reports LongHold at all.
func LongHolds() bool { return longHoldAfter > 0 }

// Event is one thing a button did.
type Event struct {
	Name Name
	Kind Kind
}

func (e Event) String() string { return string(e.Name) + " " + string(e.Kind) }

// repeats reports whether holding a button should keep acting. Volume ramps; nothing else does.
func repeats(n Name) bool { return n == VolumeUp || n == VolumeDown }

// Controller reads the buttons for the life of the process.
//
// One press usually means several things — the device acts on it, Home Assistant hears about it —
// so it is a hook rather than a callback. Listeners run on the reader goroutine and must not block:
// one that waits on something is one that stops the next button working.
type Controller struct {
	Events hook.Hook[Event]

	mu      sync.Mutex
	devices []*input.Device

	// tapped is when each button last reported a Tap, for recognising the second of a pair.
	tapped map[Name]time.Time
}

var (
	once   sync.Once
	shared *Controller
)

// Get is the buttons. There is one set.
func Get() *Controller {
	once.Do(func() { shared = &Controller{} })
	return shared
}

func (c *Controller) Name() string { return "buttons" }

// Start opens the input nodes. It is an error to find none: the buttons are the one part of the
// device that should work whatever else is wrong, so silently having none is worth a restart.
func (c *Controller) Start(context.Context) error {
	devices, err := input.List()
	if err != nil {
		return fmt.Errorf("buttons: listing input devices: %w", err)
	}
	if len(devices) == 0 {
		return fmt.Errorf("buttons: no input devices")
	}

	c.mu.Lock()
	c.devices = devices
	c.mu.Unlock()

	slog.Debug("buttons ready", "devices", len(devices))
	return nil
}

// Close releases the nodes, which is also what unblocks the readers: a read on an input node waits
// for a key, so there is no stopping one except by closing it underneath.
func (c *Controller) Close() error {
	c.mu.Lock()
	devices := c.devices
	c.devices = nil
	c.mu.Unlock()

	for _, d := range devices {
		_ = d.Close()
	}
	return nil
}

// Run reads until ctx is cancelled, or until a node fails. A failure is returned rather than logged
// so the supervisor reopens the nodes: a device that has gone away and come back is the usual reason,
// and a reader that has quietly exited leaves the device with dead buttons.
func (c *Controller) Run(ctx context.Context) error {
	c.mu.Lock()
	devices := c.devices
	c.mu.Unlock()

	failed := make(chan error, len(devices))
	for _, d := range devices {
		go func(d *input.Device) { failed <- c.watch(ctx, d) }(d)
	}

	// Returning on cancellation without waiting is deliberate: the readers are blocked in a read that
	// only Close can interrupt, and the supervisor closes after Run returns.
	select {
	case <-ctx.Done():
		return nil
	case err := <-failed:
		return err
	}
}

// held tracks one button between its press and its release. This keypad emits no autorepeat, only
// press and release, so holding is timed here rather than counted from repeat events.
type held struct {
	mu     sync.Mutex
	long   bool
	timer  *time.Timer
	longer *time.Timer
	ticker *time.Ticker
	done   chan struct{}
}

func (h *held) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.timer != nil {
		h.timer.Stop()
	}
	if h.longer != nil {
		h.longer.Stop()
	}
	if h.done != nil {
		close(h.done)
		h.done = nil
	}
	if h.ticker != nil {
		h.ticker.Stop()
	}
}

func (c *Controller) emit(name Name, kind Kind) {
	c.Events.Emit(Event{Name: name, Kind: kind})

	// A second tap is a gesture of its own, reported after the tap that completed it. Volume repeats
	// while it is held and its taps are steps rather than gestures, so it is left out.
	if kind != Tap || repeats(name) {
		return
	}
	now := time.Now()
	c.mu.Lock()
	if c.tapped == nil {
		c.tapped = map[Name]time.Time{}
	}
	last := c.tapped[name]
	// The pair is spent either way: three taps are a double and then a single, not two doubles.
	c.tapped[name] = now
	if !last.IsZero() && now.Sub(last) <= doubleTap {
		c.tapped[name] = time.Time{}
	}
	pair := !last.IsZero() && now.Sub(last) <= doubleTap
	c.mu.Unlock()

	if pair {
		c.Events.Emit(Event{Name: name, Kind: DoubleTap})
	}
}

func (c *Controller) watch(ctx context.Context, d *input.Device) error {
	down := map[uint16]*held{}
	defer func() {
		for _, h := range down {
			h.stop()
		}
	}()

	for {
		e, err := d.Read()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("buttons: reading %s: %w", d.Path, err)
		}
		if e.Type != input.EvKey {
			continue
		}
		name, ok := codes[e.Code]
		if !ok {
			continue
		}

		switch e.Value {
		case 1:
			down[e.Code] = c.pressed(name)
		case 0:
			h, ok := down[e.Code]
			if !ok {
				continue
			}
			delete(down, e.Code)
			c.released(name, h)
		}
	}
}

// pressed starts tracking a button that has just gone down.
func (c *Controller) pressed(name Name) *held {
	h := &held{}

	// A repeating button acts at once and then ramps, so a tap moves one step.
	if repeats(name) {
		c.emit(name, Tap)

		h.done = make(chan struct{})
		h.ticker = time.NewTicker(RepeatInterval)
		go func(done <-chan struct{}, t *time.Ticker) {
			for {
				select {
				case <-done:
					return
				case <-t.C:
					c.emit(name, Repeat)
				}
			}
		}(h.done, h.ticker)
	}

	h.timer = time.AfterFunc(LongPress, func() {
		h.mu.Lock()
		h.long = true
		h.mu.Unlock()

		// A repeating button keeps ramping until it is let go, and anything else may still reach a
		// long hold, so nothing is stopped here: the timer has fired, and release stops the rest.
		c.emit(name, Hold)
	})
	if longHoldAfter > 0 && !repeats(name) {
		h.longer = time.AfterFunc(longHoldAfter, func() { c.emit(name, LongHold) })
	}
	return h
}

// released finishes a button. A tap is reported here for anything that did not already report one on
// the way down, and nothing is reported for a button that was held: the hold was the event.
func (c *Controller) released(name Name, h *held) {
	h.stop()

	h.mu.Lock()
	long := h.long
	h.mu.Unlock()

	if long || repeats(name) {
		return
	}
	c.emit(name, Tap)
}
