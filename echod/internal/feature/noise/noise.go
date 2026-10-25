// Package noise is how loud the room is, as a sensor for Home Assistant: an automation can turn the
// voice down when the house is quiet at night, or notice the TV is on.
//
// Only a number leaves the microphones, and only while the switch is on, which it is not on a new
// device. The number is the room's mean level over five seconds in dBFS (hardware/mic's Noise), rounded
// to a whole decibel, and Home Assistant hears about it when it moves by two or more.
//
// It holds its last reading through anything the device does with sound. What it plays is kept out by
// the microphones' loopback; a voice request, a call or a talk through a camera, which are people
// talking to the device rather than the room, and a ring, are kept out here. With the microphones
// muted there is no room to hear, so it reads unknown until they are back.
//
// From Home Assistant all of those look alike: unknown, or a number that does not move. So whenever the
// reason there is no reading changes, the log says what it is, and says so again when readings start.
// It is once per change rather than once per window, so a device that plays music all evening logs it
// once.
package noise

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

func init() {
	component.Register(component.Device, Get(), component.Order(6))
}

const (
	// windowLength is how long each reading averages over.
	windowLength = 5 * time.Second

	// look is how often, inside a window, the device is asked whether it is busy: often enough that a
	// short request between two looks is still seen.
	look = time.Second

	// moved is how far a reading has to be from the last one sent before it is sent, in dB.
	moved = 2
)

// Noise is the switch and the sensor.
type Noise struct {
	on    *esphome.Switch
	level *esphome.Sensor

	// busy says whether the device is doing something that is not the room, muted whether the
	// microphones are cut, read is the room's level since it was last asked or why there is none, and
	// say puts a change in that reason in the log; replaced in tests.
	busy  func() bool
	muted func() bool
	read  func() (float64, string)
	say   func(why string, value float64)

	mu      sync.Mutex
	enabled bool
	last    held

	// why is the reason the last window gave no reading, empty for a reading, and told whether the
	// log has had one since the switch last moved.
	why  string
	told bool
}

// window is the reading being gathered: when it started, and whether the device was busy during it.
type window struct {
	started time.Time
	busy    bool
}

// held is the reading Home Assistant has, if it has one.
type held struct {
	value float64
	known bool
}

var (
	once   sync.Once
	shared *Noise
)

// Get is the sensor.
func Get() *Noise {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Noise {
	n := &Noise{
		on: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "ambient_noise_sensor",
				Name:     "Ambient noise sensor",
				Icon:     "mdi:ear-hearing",
				Category: esphome.CategoryConfig,
				DeviceID: component.DeviceMicrophone,
			},
		},
		level: &esphome.Sensor{
			Base: esphome.Base{
				ObjectID: "ambient_noise",
				Name:     "Ambient noise",
				Icon:     "mdi:ear-hearing",
				DeviceID: component.DeviceMicrophone,
			},
			Unit:       "dBFS",
			StateClass: esphome.StateClassMeasurement,
		},
		busy:  busy,
		muted: muted,
		read:  func() (float64, string) { return mic.Get().Noise() },
		say:   say,
	}

	n.on.OnCommand = func(on bool) {
		n.set(on)
		if err := config.Set().Microphone().Noise(on); err != nil {
			slog.Error("saving the ambient noise sensor failed", "err", err)
		}
	}
	return n
}

// busy is whether the device is in the middle of something whose sound is not the room's.
func busy() bool {
	return voice.Get().Busy() || phone.Get().Busy() || talkback.Get().Busy() || ring.IsSounding()
}

// say logs why the sensor has no reading, or that it has one again.
func say(why string, value float64) {
	if why == "" {
		slog.Info("ambient noise measuring", "dbfs", value)
		return
	}
	slog.Info("ambient noise has no reading", "why", why)
}

// muted is whether the microphones are cut. One whose mute cannot be read counts as live, as it does
// for the wake word.
func muted() bool {
	m, err := mute.Get().Muted()
	return err == nil && m
}

// set turns measuring on or off. Off, Home Assistant is told there is no reading rather than left with
// the last one, which would read as a room that never changes.
func (n *Noise) set(on bool) {
	n.on.Set(on)
	mic.Get().SetNoise(on)
	n.mu.Lock()
	n.enabled = on
	n.told = false
	n.mu.Unlock()
	if !on {
		n.forget()
	}
}

// forget drops the reading Home Assistant has and tells it there is none.
func (n *Noise) forget() {
	n.mu.Lock()
	n.last = held{}
	n.mu.Unlock()
	n.level.Set(float32(math.NaN()))
}

func (n *Noise) Name() string { return "ambient noise" }

func (n *Noise) Entities() []esphome.Entity { return []esphome.Entity{n.on, n.level} }

func (n *Noise) Restore(c config.Config) {
	n.set(c.Microphone.Noise)
	slog.Info("restored", "what", n.on.ObjectID, "using", c.Microphone.Noise)
}

// Run takes a reading every window while the switch is on.
func (n *Noise) Run(ctx context.Context) error {
	tick := time.NewTicker(look)
	defer tick.Stop()

	w := window{started: time.Now()}
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-tick.C:
			n.beat(now, &w)
		}
	}
}

// beat is one look: nothing while the switch is off, unknown while the microphones are cut, and a
// reading at the end of each window otherwise.
func (n *Noise) beat(now time.Time, w *window) {
	n.mu.Lock()
	on, known := n.enabled, n.last.known
	n.mu.Unlock()

	if !on {
		*w = window{started: now}
		return
	}
	if n.muted() {
		// Said once, and the window thrown away, so what was heard before the mute is not a reading after.
		if known {
			n.forget()
		}
		n.read()
		n.explain(mutedWhy, 0)
		*w = window{started: now}
		return
	}

	w.busy = w.busy || n.busy()
	if now.Sub(w.started) < windowLength {
		return
	}
	v, why := n.read()
	if w.busy {
		// The device's own business comes first: a reply is played as well as listened to, and what
		// was played is the consequence, not the reason.
		why = busyWhy
	}
	n.take(v, why == "")
	n.explain(why, math.Round(v))
	*w = window{started: now}
}

// The reasons a window gives no reading that are this package's own; the microphones' are mic.Noise*.
const (
	mutedWhy = "the microphones are muted"
	busyWhy  = "the device was busy with a request, a call, a talk or a ring"
)

// explain tells the log why there is no reading, or that there is one, when that is not what it last
// said.
func (n *Noise) explain(why string, value float64) {
	n.mu.Lock()
	same := n.told && n.why == why
	n.why, n.told = why, true
	n.mu.Unlock()
	if !same {
		n.say(why, value)
	}
}

// take is one window's reading: sent when there is one and it moved far enough from the last, held
// otherwise.
func (n *Noise) take(v float64, ok bool) {
	n.mu.Lock()
	next, send := step(n.last, v, ok)
	n.last = next
	n.mu.Unlock()
	if send {
		n.level.Set(float32(next.value))
	}
}

// step decides what a reading does to the one Home Assistant has: the next held reading, and whether to
// send it.
func step(last held, v float64, ok bool) (held, bool) {
	if !ok {
		return last, false
	}
	v = math.Round(v)
	if last.known && math.Abs(v-last.value) < moved {
		return last, false
	}
	return held{value: v, known: true}, true
}
