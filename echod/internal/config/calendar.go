package config

// Calendar is which of Home Assistant's calendars this device shows (docs/calendar-and-night-plan.md).
// Each device keeps its own: one on a desk shows its owner's, one in a kitchen the family's.
type Calendar struct {
	// Sources are calendar entities, in the order they were chosen. None: no calendar on this device.
	Sources []string `json:"sources,omitempty"`

	// Popups puts an event coming up on the screen. Off unless somebody turns it on.
	Popups bool `json:"popups,omitempty"`

	// PopupCalendars are the calendars whose events pop up; none is every one this device shows.
	PopupCalendars []string `json:"popup_calendars,omitempty"`

	// PopupBefore is how many minutes before an event it pops up: 0 for the default quarter hour, -1
	// for as it starts.
	PopupBefore int `json:"popup_before,omitempty"`

	// PopupSilent pops up without the chime.
	PopupSilent bool `json:"popup_silent,omitempty"`

	// PopupAllDayNever leaves all-day events alone; otherwise each pops up once, in the morning.
	PopupAllDayNever bool `json:"popup_all_day_never,omitempty"`

	// PopupShown are the events already popped up, so a restart does not pop them up again: the
	// calendar, start and title of each, for two days.
	PopupShown []string `json:"popup_shown,omitempty"`
}

// PopupDefaultBefore is PopupBefore's default, in minutes.
const PopupDefaultBefore = 15

// PopupLead is how many minutes before an event it pops up.
func (c Calendar) PopupLead() int {
	switch {
	case c.PopupBefore < 0:
		return 0
	case c.PopupBefore == 0:
		return PopupDefaultBefore
	}
	return c.PopupBefore
}

type CalendarWriter struct{ st *Store }

func (w CalendarWriter) Sources(v []string) error {
	return w.st.Update(func(c *Config) { c.Calendar.Sources = v })
}

func (w CalendarWriter) Popups(v bool) error {
	return w.st.Update(func(c *Config) { c.Calendar.Popups = v })
}

func (w CalendarWriter) PopupCalendars(v []string) error {
	return w.st.Update(func(c *Config) { c.Calendar.PopupCalendars = v })
}

// PopupLead sets how many minutes before an event it pops up; 0 is as it starts.
func (w CalendarWriter) PopupLead(minutes int) error {
	v := minutes
	switch {
	case minutes <= 0:
		v = -1
	case minutes == PopupDefaultBefore:
		v = 0
	}
	return w.st.Update(func(c *Config) { c.Calendar.PopupBefore = v })
}

func (w CalendarWriter) PopupSilent(v bool) error {
	return w.st.Update(func(c *Config) { c.Calendar.PopupSilent = v })
}

func (w CalendarWriter) PopupShown(v []string) error {
	return w.st.Update(func(c *Config) { c.Calendar.PopupShown = v })
}

func (w CalendarWriter) PopupAllDayNever(v bool) error {
	return w.st.Update(func(c *Config) { c.Calendar.PopupAllDayNever = v })
}
