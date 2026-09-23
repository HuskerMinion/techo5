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
}

type TimersWriter struct{ st *Store }

// Local replaces the remembered timers. Written when one is set, cancelled or finishes, and never on
// a tick: every set marshals the whole config and fsyncs it.
func (w TimersWriter) Local(list []LocalTimer) error {
	return w.st.Update(func(c *Config) { c.Timers.Local = slices.Clone(list) })
}
