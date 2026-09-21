package config

import (
	"fmt"
	"slices"
	"strings"
)

// Alarms are the device's own alarms, and the Home Assistant helpers it follows as alarms.
type Alarms struct {
	List []Alarm `json:"list,omitempty"`

	// Follow is Home Assistant helpers to ring at: an input_datetime, optionally with an entity
	// whose "on" state arms it, written "input_datetime.wake=input_boolean.wake_on".
	Follow []string `json:"follow,omitempty"`

	// SnoozeMinutes is how long Snooze puts an alarm off.
	SnoozeMinutes int `json:"snooze_minutes,omitempty"`

	// Sound is what an alarm rings with, by name; empty is the first of the speaker's alarm sounds.
	Sound string `json:"sound,omitempty"`

	// SunriseMinutes is how long before an alarm the screen starts to light, nothing for not at all.
	// The light comes up from almost nothing to the brightness the screen is set to, so the room is
	// lit before the sound starts.
	SunriseMinutes int `json:"sunrise_minutes,omitempty"`

	// SunriseFace draws the sun with a face on it, which is a matter of taste rather than of waking up.
	SunriseFace bool `json:"sunrise_face,omitempty"`
}

// Alarm is one alarm set on the device.
type Alarm struct {
	ID     string `json:"id"`
	Hour   int    `json:"hour"`
	Minute int    `json:"minute"`
	// Days is which weekdays it repeats on, bit 0 Sunday through bit 6 Saturday; none means once.
	Days  uint8  `json:"days,omitempty"`
	Label string `json:"label,omitempty"`
	On    bool   `json:"on"`
}

const (
	DefaultSnoozeMinutes = 9
	MinSnoozeMinutes     = 1
	MaxSnoozeMinutes     = 30
)

// Snooze is the snooze length, with the default for a device that never set one.
func (a Alarms) Snooze() int {
	if a.SnoozeMinutes <= 0 {
		return DefaultSnoozeMinutes
	}
	return a.SnoozeMinutes
}

// Named sets of days.
const (
	DaysOnce     uint8 = 0
	DaysEvery    uint8 = 0x7f
	DaysWeekdays uint8 = 0x3e
	DaysWeekends uint8 = 0x41
)

var dayNames = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// ParseDays reads "daily", "weekdays", "weekends", "once" or a list such as "mon,wed,fri".
func ParseDays(s string) (uint8, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "once":
		return DaysOnce, nil
	case "daily", "every day", "everyday":
		return DaysEvery, nil
	case "weekdays":
		return DaysWeekdays, nil
	case "weekends":
		return DaysWeekends, nil
	}
	var days uint8
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		i := slices.IndexFunc(dayNames[:], func(d string) bool { return strings.HasPrefix(part, d) })
		if i < 0 {
			return 0, fmt.Errorf("alarms: %q is not a day", part)
		}
		days |= 1 << i
	}
	return days, nil
}

// DaysLabel is how a set of days reads on the screen.
func DaysLabel(days uint8) string {
	switch days & DaysEvery {
	case DaysOnce:
		return "once"
	case DaysEvery:
		return "every day"
	case DaysWeekdays:
		return "weekdays"
	case DaysWeekends:
		return "weekends"
	}
	var out []string
	for i, d := range dayNames {
		if days&(1<<i) != 0 {
			out = append(out, strings.ToUpper(d[:1])+d[1:])
		}
	}
	return strings.Join(out, " ")
}

type AlarmsWriter struct{ st *Store }

// Put adds an alarm, or replaces the one with its ID.
func (w AlarmsWriter) Put(a Alarm) error {
	return w.st.Update(func(c *Config) {
		if i := slices.IndexFunc(c.Alarms.List, func(x Alarm) bool { return x.ID == a.ID }); i >= 0 {
			c.Alarms.List[i] = a
			return
		}
		c.Alarms.List = append(c.Alarms.List, a)
	})
}

func (w AlarmsWriter) Delete(id string) error {
	return w.st.Update(func(c *Config) {
		c.Alarms.List = slices.DeleteFunc(c.Alarms.List, func(x Alarm) bool { return x.ID == id })
	})
}

// SnoozeMinutes sets the snooze length, held between MinSnoozeMinutes and MaxSnoozeMinutes.
func (w AlarmsWriter) SnoozeMinutes(n int) error {
	n = min(max(n, MinSnoozeMinutes), MaxSnoozeMinutes)
	return w.st.Update(func(c *Config) { c.Alarms.SnoozeMinutes = n })
}

// Sound sets what alarms ring with, by name.
func (w AlarmsWriter) SunriseMinutes(n int) error {
	return w.st.Update(func(c *Config) { c.Alarms.SunriseMinutes = n })
}

func (w AlarmsWriter) SunriseFace(on bool) error {
	return w.st.Update(func(c *Config) { c.Alarms.SunriseFace = on })
}

func (w AlarmsWriter) Sound(name string) error {
	return w.st.Update(func(c *Config) { c.Alarms.Sound = name })
}

func (w AlarmsWriter) Follow(entities []string) error {
	return w.st.Update(func(c *Config) { c.Alarms.Follow = entities })
}
