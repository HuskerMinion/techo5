//go:build !dot

package presence

import (
	esphome "github.com/ygelfand/go-esphome-device"

	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

// A looker is a detector of a board's own (a face detector in its SoC, say) that looks at the frames
// presence watches, beside the movement detector. It registers from a file of its own (an init). It
// may keep the screen awake (saw) without being movement: the Motion sensor stays movement's alone.
type looker struct {
	entities func() []esphome.Entity // its switches and sensors, where the board has it
	restore  func(c config.Config)
	on       func() bool // the camera is to watch for it
	// watch begins a watch: frame is each frame of it, done lets the detector go at its end.
	watch func() (frame func(*camera.Frame), done func())
	ended func() // a watch is over: what it shows is cleared, after the Motion sensor
}

var lookers []looker

func lookerEntities() []esphome.Entity {
	var e []esphome.Entity
	for _, l := range lookers {
		e = append(e, l.entities()...)
	}
	return e
}

func restoreLookers(c config.Config) {
	for _, l := range lookers {
		l.restore(c)
	}
}

func lookersOn() bool {
	for _, l := range lookers {
		if l.on() {
			return true
		}
	}
	return false
}

// startLookers begins a watch for every looker: look is each frame, done their end.
func startLookers() (look func(*camera.Frame), done func()) {
	var frames []func(*camera.Frame)
	var dones []func()
	for _, l := range lookers {
		frame, done := l.watch()
		frames, dones = append(frames, frame), append(dones, done)
	}
	return func(fr *camera.Frame) {
			for _, f := range frames {
				f(fr)
			}
		}, func() {
			for _, d := range dones {
				d()
			}
		}
}

func lookersEnded() {
	for _, l := range lookers {
		l.ended()
	}
}

// saw notes that a looker saw what (somebody's face, say) in a frame taken at: it keeps the screen
// awake for hold, and leaves the room's movement, and with it the Motion sensor, alone.
func (f *Feature) saw(at time.Time, what string) {
	f.mu.Lock()
	was := f.seenNear(at)
	f.seenAt = at
	f.mu.Unlock()
	if !was {
		slog.Info("presence: " + what + " near")
		f.Changed.Emit(struct{}{})
	}
}

// seenNear is whether a looker saw somebody within hold of now, with f.mu held.
func (f *Feature) seenNear(now time.Time) bool {
	return !f.seenAt.IsZero() && now.Sub(f.seenAt) <= hold
}
