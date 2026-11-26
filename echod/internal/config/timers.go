package config

import (
	"slices"
	"time"
)

// Timers is what the device remembers of its own timers.
//
// Only its own. Home Assistant's timers are Home Assistant's: it holds them in memory and comes back
// without them, so a timer of its that were written down here would be a timer nobody else believes
// in — it would ring, and no one could say what for. A timer set on the device finishes from this
// clock and is meant to run with Home Assistant away, so it is the one that is worth keeping.
type Timers struct {
	Local []LocalTimer `json:"local,omitempty"`

	// InHAList puts the device's own timers in Home Assistant's timer list too (Home Assistant 2026.12
	// and later), where dashboards, automations and voice see them beside Home Assistant's. Off unless
	// asked for: it needs "Allow the device to perform Home Assistant actions", and with that off every
	// timer set here would raise a repair in Home Assistant.
	InHAList bool `json:"in_ha_list,omitempty"`
}

// LocalTimer is one timer set on the device, as an absolute finish time rather than a duration left.
// A duration left means nothing after the process stops; a finish time means the same thing however
// long the device was off and whatever the clock did in between.
type LocalTimer struct {
	ID    string        `json:"id"`
	Name  string        `json:"name,omitempty"`
	Total time.Duration `json:"total"`

	// Finish is when it goes off. Zero for a timer that is paused, where Left is what it has to run.
	Finish time.Time     `json:"finish,omitempty"`
	Left   time.Duration `json:"left,omitempty"`

	// HA is the id of the timer standing for this one in Home Assistant's timer list, while it has one.
	HA string `json:"ha,omitempty"`
}

type TimersWriter struct{ st *Store }

// Local replaces the remembered timers. Written when one is set, canceled or finishes, and never on
// a tick: every set marshals the whole config and fsyncs it.
func (w TimersWriter) Local(list []LocalTimer) error {
	return w.st.Update(func(c *Config) { c.Timers.Local = slices.Clone(list) })
}

// InHAList saves whether the device's own timers go in Home Assistant's timer list.
func (w TimersWriter) InHAList(v bool) error {
	return w.st.Update(func(c *Config) { c.Timers.InHAList = v })
}
