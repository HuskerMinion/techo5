package config

// Calendar is which of Home Assistant's calendars this device shows (docs/calendar-and-night-plan.md).
// Each device keeps its own: one on a desk shows its owner's, one in a kitchen the family's.
type Calendar struct {
	// Sources are calendar entities, in the order they were chosen. None: no calendar on this device.
	Sources []string `json:"sources,omitempty"`
}

type CalendarWriter struct{ st *Store }

func (w CalendarWriter) Sources(v []string) error {
	return w.st.Update(func(c *Config) { c.Calendar.Sources = v })
}
