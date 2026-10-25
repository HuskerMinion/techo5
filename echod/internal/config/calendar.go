package config

// Calendar is which of Home Assistant's calendars this device shows (docs/calendar-and-night-plan.md).
// Each device keeps its own: one on a desk shows its owner's, one in a kitchen the family's.
type Calendar struct {
	// Sources are calendar entities, in the order they were chosen, and the Links as "ical.<id>". None:
	// no calendar on this device.
	Sources []string `json:"sources,omitempty"`

	// Links are calendars read from their iCal address rather than from Home Assistant: what Google,
	// iCloud and Outlook give as a calendar's private address. The address is a secret - anyone who has
	// it can read the calendar - so it is never shown again once saved.
	Links []CalendarLink `json:"links,omitempty"`

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

	// How the calendar page and the clock show the events (display/calendar_look.go). Each is on unless
	// somebody turns it off, so a saved file without them has them all: DayList draws a day as the list
	// it was rather than on its hours, AgendaPlain draws Coming up in its two-line rows, NoIcons leaves
	// out the small pictures before titles, NoCountdowns the "in 25 min" chips, NoNextUp the next event
	// at the foot of the clock, and NoWeather the forecast on the calendar's days.
	DayList      bool `json:"day_list,omitempty"`
	AgendaPlain  bool `json:"agenda_plain,omitempty"`
	NoIcons      bool `json:"no_icons,omitempty"`
	NoCountdowns bool `json:"no_countdowns,omitempty"`
	NoNextUp     bool `json:"no_next_up,omitempty"`
	NoWeather    bool `json:"no_weather,omitempty"`

	// Week is the week view, "on" or "off", and MonthDetails the month's, "full" or "simple". Empty is
	// the panel's own default (on and full on a screen bigger than the Show 5's, off and simple on the
	// Show 5), kept empty until somebody chooses, so a change to that default still reaches them.
	Week         string `json:"week,omitempty"`
	MonthDetails string `json:"month_details,omitempty"`

	// Opens is the view the calendar opens on, from the clock's date, a voice request or calendar_show:
	// "week", "day" (today) or "agenda" (Coming up). Empty is the month. The clock's next event still
	// opens Coming up, and an event's day still opens that day.
	Opens string `json:"opens,omitempty"`

	// Hidden are the calendars turned off with the calendar page's chips: their events are left out of
	// this screen's calendar and the clock's next event. This device's alone, like the calendars it
	// shows: never shared, and a settings file neither carries nor changes it (KeepOwn).
	Hidden []string `json:"hidden,omitempty"`
}

// The values Week and MonthDetails take besides empty.
const (
	CalendarOn  = "on"
	CalendarOff = "off"
	MonthFull   = "full"
	MonthSimple = "simple"
)

// The views Opens takes besides empty, the month.
const (
	OpensWeek   = "week"
	OpensDay    = "day"
	OpensAgenda = "agenda"
)

// WeekOn is whether the week view is on, wide saying the panel is bigger than the Show 5's.
func (c Calendar) WeekOn(wide bool) bool {
	switch c.Week {
	case CalendarOn:
		return true
	case CalendarOff:
		return false
	}
	return wide
}

// MonthFullOn is whether the month shows its details in full, wide as for WeekOn.
func (c Calendar) MonthFullOn(wide bool) bool {
	switch c.MonthDetails {
	case MonthFull:
		return true
	case MonthSimple:
		return false
	}
	return wide
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

// CalendarLink is one calendar read from its iCal address.
type CalendarLink struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// CalendarLinkPrefix begins a Links calendar's id among the Sources.
const CalendarLinkPrefix = "ical."

type CalendarWriter struct{ st *Store }

func (w CalendarWriter) Links(v []CalendarLink) error {
	return w.st.Update(func(c *Config) { c.Calendar.Links = v })
}

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

// The calendar's look, each switch as its own writer like the pop-ups'.

func (w CalendarWriter) DayList(v bool) error {
	return w.st.Update(func(c *Config) { c.Calendar.DayList = v })
}

func (w CalendarWriter) AgendaPlain(v bool) error {
	return w.st.Update(func(c *Config) { c.Calendar.AgendaPlain = v })
}

func (w CalendarWriter) NoIcons(v bool) error {
	return w.st.Update(func(c *Config) { c.Calendar.NoIcons = v })
}

func (w CalendarWriter) NoCountdowns(v bool) error {
	return w.st.Update(func(c *Config) { c.Calendar.NoCountdowns = v })
}

func (w CalendarWriter) NoNextUp(v bool) error {
	return w.st.Update(func(c *Config) { c.Calendar.NoNextUp = v })
}

func (w CalendarWriter) NoWeather(v bool) error {
	return w.st.Update(func(c *Config) { c.Calendar.NoWeather = v })
}

// Week turns the week view on or off; the choice is kept from then on, whatever the panel's default.
func (w CalendarWriter) Week(on bool) error {
	v := CalendarOff
	if on {
		v = CalendarOn
	}
	return w.st.Update(func(c *Config) { c.Calendar.Week = v })
}

// MonthDetails chooses the month's details in full or simple, kept as Week is.
func (w CalendarWriter) MonthDetails(full bool) error {
	v := MonthSimple
	if full {
		v = MonthFull
	}
	return w.st.Update(func(c *Config) { c.Calendar.MonthDetails = v })
}

// Opens sets the view the calendar opens on: one of the Opens values, or empty for the month.
func (w CalendarWriter) Opens(v string) error {
	return w.st.Update(func(c *Config) { c.Calendar.Opens = v })
}

func (w CalendarWriter) Hidden(v []string) error {
	return w.st.Update(func(c *Config) { c.Calendar.Hidden = v })
}
