package ring

import (
	"slices"
	"strings"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// What Home Assistant is told about a ring. On means a ring is active, not that it is audible: one a
// button silenced is still waiting on its snooze offer, and an automation that dims the lights for
// an alarm wants them dimmed until the alarm is answered.
//
// The flags are per kind as well as combined so "when the timer rings" is a state trigger, where
// reading it off the words would be a template.
var (
	sounding = &esphome.BinarySensor{
		Base: esphome.Base{ObjectID: "sounding", Name: "Sounding", Icon: "mdi:bell-ring"},
	}
	alarmSounding = &esphome.BinarySensor{
		Base: esphome.Base{ObjectID: "alarm_sounding", Name: "Alarm sounding", Icon: "mdi:alarm-bell"},
	}
	timerSounding = &esphome.BinarySensor{
		Base: esphome.Base{ObjectID: "timer_sounding", Name: "Timer sounding", Icon: "mdi:timer-alert-outline"},
	}
	soundingWhat = &esphome.TextSensor{
		Base: esphome.Base{ObjectID: "sounding_what", Name: "Sounding what", Icon: "mdi:bell-badge-outline"},
	}
)

type soundingFeature struct{}

func (soundingFeature) Name() string { return "sounding" }
func (soundingFeature) Entities() []esphome.Entity {
	return []esphome.Entity{sounding, alarmSounding, timerSounding, soundingWhat}
}

// Restore has the sensors written from whatever is ringing, which at start-up is nothing: they read
// off rather than unknown. It asks the publisher rather than writing, so a restore that ran while a
// ring was active would leave the sensors on.
func (soundingFeature) Restore(config.Config) { poke() }

func init() { component.Register(component.Device, soundingFeature{}, component.Order(34)) }

// active is the rings the sensors describe, in the order they began. Pointers, so two rings of the
// same kind and label are still two.
var active struct {
	mu   sync.Mutex
	list []*activeRing
}

type activeRing struct{ what, label string }

func (r *activeRing) says() string {
	if r.label == "" {
		return r.what
	}
	return r.what + ` "` + r.label + `"`
}

// begin puts a ring on the sensors and returns the call that takes it off. It only changes the list
// and tells the publisher; it never writes a sensor itself. Writing one sends to every Home Assistant
// connection and can wait on a dead one for as long as the connection's write timeout, and the alarm
// and the timer call Start with their own locks held, so a write here would hold up a ring starting,
// and the Stop that waits on the same lock.
func begin(what, label string) (leave func()) {
	r := &activeRing{what: what, label: label}
	active.mu.Lock()
	active.list = append(active.list, r)
	active.mu.Unlock()
	poke()

	var once sync.Once
	return func() {
		once.Do(func() {
			active.mu.Lock()
			active.list = slices.DeleteFunc(active.list, func(x *activeRing) bool { return x == r })
			active.mu.Unlock()
			poke()
		})
	}
}

var (
	// wake has room for one: a burst of changes is one write of the latest list, not one each.
	wake      = make(chan struct{}, 1)
	publisher sync.Once
)

// poke asks the publisher to write the sensors from the list as it is by then.
func poke() {
	publisher.Do(func() { safe.Go("sounding sensors", publishLoop) })
	select {
	case wake <- struct{}{}:
	default:
	}
}

// publishLoop is the only thing that writes the sensors, so nothing on the ring path waits on the
// network. It writes the latest list each time, so the sensors end up right however many changes
// were folded together or however long one write took.
func publishLoop() {
	for range wake {
		active.mu.Lock()
		list, set := slices.Clone(active.list), setSensors
		active.mu.Unlock()
		set(list)
	}
}

// setSensors is what the sensors are written with, read and replaced under active.mu.
var setSensors = publish

// publish writes the sensors from the rings given. Only the publisher calls it, with no lock held.
func publish(list []*activeRing) {
	var alarm, timer bool
	says := make([]string, 0, len(list))
	for _, r := range list {
		alarm = alarm || r.what == "alarm"
		timer = timer || r.what == "timer"
		says = append(says, r.says())
	}
	sounding.Set(len(list) > 0)
	alarmSounding.Set(alarm)
	timerSounding.Set(timer)
	soundingWhat.Set(strings.Join(says, ", "))
}
