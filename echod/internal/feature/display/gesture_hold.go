//go:build !dot && !spot

package display

import (
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/presence"
)

// Hold on the clock: a finger held on the clock for half a second tells Home Assistant, which turns it into
// whatever the household wants (the hall light, the TV off), as the palm over the camera and the double
// clap do. Off unless somebody turns it on.
//
// The Show's screen does not report a finger held still unless something asks (touch.SetHolds): this asks
// only while the plain clock is up and the switch is on, so every other page keeps its taps as they were,
// and a slow tap on the clock is the one that becomes a hold.

func init() {
	component.Register(component.Device, holdGesture(), component.Order(61))
}

// clockHold is the switch.
type clockHold struct{ on *esphome.Switch }

var (
	holdOnce   sync.Once
	holdShared *clockHold
)

func holdGesture() *clockHold {
	holdOnce.Do(func() {
		h := &clockHold{on: &esphome.Switch{Base: esphome.Base{
			ObjectID: "clock_hold_gesture", Name: "Hold on the clock", Icon: "mdi:gesture-tap-hold",
			Category: esphome.CategoryConfig,
		}}}
		h.on.OnCommand = func(on bool) {
			h.on.Set(on)
			if err := config.Set().Screen().ClockHold(on); err != nil {
				slog.Error("saving hold on the clock failed", "err", err)
			}
			Get().wake()
		}
		holdShared = h
	})
	return holdShared
}

func (h *clockHold) Name() string { return "hold on the clock" }

func (h *clockHold) Entities() []esphome.Entity { return []esphome.Entity{h.on} }

func (h *clockHold) Restore(c config.Config) { h.on.Set(c.Screen.ClockHold) }

// plainClock is whether a frame is the plain clock and nothing over it: not the screensaver, the light
// before an alarm, the night clock, the drawer, a card, an announcement, the setup page asking or the
// PIN. boring is the frame having no page of its own.
func plainClock(s *scene, boring bool) bool {
	return boring && s.sunrise == 0 && s.slideshowScreensaver == nil && !s.redClock && !s.showDrawer &&
		!s.setupAsking && !s.pin.open && !s.showNotification && !s.showReminder && s.popup == nil &&
		!s.showAnnouncement
}

// clockHoldScene says whether the plain clock is what is up, and so whether holds are wanted for it.
func (d *Display) clockHoldScene(plainClock bool) {
	want := plainClock && config.Get().Screen.ClockHold
	d.mu.Lock()
	changed := want != d.clockHolds
	d.clockHolds = want
	d.mu.Unlock()
	if changed {
		d.applyHolds()
	}
}

// fireGesture tells Home Assistant of a gesture made on the screen; a variable for the tests.
var fireGesture = func(name string) {
	component.Fire.Emit(component.Event{Name: presence.GestureEvent, Data: map[string]string{
		"gesture": name,
		"device":  config.Get().Device.Name,
	}})
}

// clockHeld is a finger held on the clock: told to Home Assistant, while the clock asked for holds and
// nothing a tap would go to is on it: an answer's words, a wake light held at full, the weather.
func (d *Display) clockHeld() {
	now := time.Now()
	d.mu.Lock()
	on := d.clockHolds && pageIdle(d.view.Phase) && !now.Before(d.weatherUntil)
	d.mu.Unlock()
	if !on || d.answerUp(now) || d.wakeLight(now) > 0 {
		return
	}
	slog.Info("screen: a finger held on the clock")
	fireGesture("hold")
}
