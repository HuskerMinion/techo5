package ring

import (
	"slices"
	"strings"
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
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

func (soundingFeature) Restore(config.Config) { publish(nil) }

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

// begin puts a ring on the sensors and returns the call that takes it off. Called with no lock of
// the bell's held: setting a sensor only records state and tells Home Assistant, and must never be
// able to hold up a ring or its stop.
func begin(what, label string) (leave func()) {
	r := &activeRing{what: what, label: label}
	active.mu.Lock()
	defer active.mu.Unlock()
	active.list = append(active.list, r)
	publish(active.list)

	var once sync.Once
	return func() {
		once.Do(func() {
			active.mu.Lock()
			defer active.mu.Unlock()
			active.list = slices.DeleteFunc(active.list, func(x *activeRing) bool { return x == r })
			publish(active.list)
		})
	}
}

// publish writes the sensors from the rings given; active.mu is held, or there is nothing to race.
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
