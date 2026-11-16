//go:build !dot

// Package touched tells Home Assistant that somebody is using the screen: a Touched binary sensor
// that comes on as a finger lands and goes off a while after the last one lifted.
//
// Home Assistant sees a turn and the screen's light, but not a finger on the panel, so an automation
// that puts the screen out after a while of no voice turns would put it out under somebody tapping
// through a dashboard. This is what such an automation waits on ("Touched off for 5 minutes").
//
// A touch counts wherever it lands and whatever it does: one that only lights a dark screen, a swipe,
// a finger dragging a map. The sensor stays on while a finger is down and for hold after it lifts, so
// somebody tapping away keeps it on rather than turning it on and off at every tap. What it carries is
// only that: not where, not what was touched.
package touched

import (
	"context"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/service"
)

func init() {
	component.Register(component.Device, Get(), component.Order(84),
		component.Supervise(service.Restart(time.Second, 30*time.Second)))
}

// hold is how long after the last finger lifted the screen still counts as in use: the gap between
// two taps of somebody reading and tapping on.
const hold = 30 * time.Second

// Feature is the sensor and the loop that writes it.
type Feature struct {
	sensor *esphome.BinarySensor
}

var (
	once   sync.Once
	shared *Feature
)

// Get is the feature.
func Get() *Feature {
	once.Do(func() {
		shared = &Feature{sensor: &esphome.BinarySensor{
			Base:        esphome.Base{ObjectID: "screen_touched", Name: "Touched", Icon: "mdi:gesture-tap"},
			DeviceClass: "occupancy",
		}}
	})
	return shared
}

func (f *Feature) Name() string { return "touched" }

func (f *Feature) Entities() []esphome.Entity { return []esphome.Entity{f.sensor} }

// Restore has the sensor read off rather than unknown until the first touch.
func (f *Feature) Restore(config.Config) { f.sensor.Set(false) }

func (f *Feature) Run(ctx context.Context) error {
	return run(ctx, screen{touch.Get()}, f.sensor.Set, hold)
}

// source is the touchscreen as the loop needs it.
type source interface {
	// Touched is whether a finger is on the panel now.
	Touched() bool
	// Listen calls fn as fingers land (true) and lift (false), and returns the function that stops it.
	Listen(fn func(down bool)) (cancel func())
}

type screen struct{ s *touch.Screen }

func (c screen) Touched() bool                        { return c.s.Touched() }
func (c screen) Listen(fn func(bool)) (cancel func()) { return c.s.Down.Listen(fn) }

// state is the sensor's value and when it goes off.
type state struct {
	on    bool
	until time.Time // when it goes off; zero while a finger is down or while it is off
}

// look takes what the panel did since the last look: landed is a finger having come down (it may be
// up again already: a quick tap lands and lifts before the loop gets to read it), down is a finger on
// it now. It reports whether the value changed.
func (s *state) look(now time.Time, landed, down bool, hold time.Duration) bool {
	was := s.on
	if landed || down {
		s.on = true
	}
	switch {
	case !s.on:
	case down:
		s.until = time.Time{}
	default:
		// The loop only looks when a finger lands or lifts, so a finger that is not down has just
		// lifted: the hold starts over from now.
		s.until = now.Add(hold)
	}
	return s.on != was
}

// expire turns the sensor off once its hold has run out with no finger down, and reports whether it
// did.
func (s *state) expire(now time.Time, down bool) bool {
	if !s.on || down || s.until.IsZero() || now.Before(s.until) {
		return false
	}
	s.on, s.until = false, time.Time{}
	return true
}

// run writes the sensor from the panel until ctx ends. It is the only writer: the touch listener runs
// on the touch reader's goroutine and must not block, while a write goes to every Home Assistant
// connection and can wait on a dead one, so the listener only notes the finger and wakes the loop.
func run(ctx context.Context, src source, set func(bool), hold time.Duration) error {
	var mu sync.Mutex
	landed := false
	wake := make(chan struct{}, 1)
	cancel := src.Listen(func(down bool) {
		if down {
			mu.Lock()
			landed = true
			mu.Unlock()
		}
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	defer cancel()

	// A restart starts from off, whatever the last run left on the sensor.
	var s state
	set(false)

	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	arm := func(now time.Time) {
		if s.until.IsZero() {
			timer.Stop()
			return
		}
		timer.Reset(s.until.Sub(now))
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
			mu.Lock()
			l := landed
			landed = false
			mu.Unlock()
			now := time.Now()
			if s.look(now, l, src.Touched(), hold) {
				set(s.on)
			}
			arm(now)
		case <-timer.C:
			now := time.Now()
			if s.expire(now, src.Touched()) {
				set(false)
			}
			arm(now)
		}
	}
}
