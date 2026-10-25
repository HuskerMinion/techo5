//go:build !dot && !spot

package display

import (
	"log/slog"
	"slices"
	"sync/atomic"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
)

// How the calendar looks (docs/calendar-and-night-plan.md, 6): eight switches, each on the screen
// under Settings, Display, Calendar, on the setup page's Weather & Calendar tab, and in Home Assistant,
// where Shared settings can keep them the same on every screen in the house. Two go by the panel's
// size until somebody chooses: the week view and the month in full are on where there is room for
// them, bigger than the Show 5, and off on the Show 5. Which calendars are hidden is the ninth, set on
// the calendar page itself and this screen's alone.

// calLook is the switches as this frame draws them, the panel's size worked in.
type calLook struct {
	timeline   bool // a day on its hours, rather than as a list
	refined    bool // Coming up in one line an event, its days headed by their weather
	monthFull  bool // the month with times, icons and weather, rather than its bars alone
	icons      bool // a small picture before a title, by a word in it
	countdowns bool // "in 25 min" on today's events, and the days to a long event on the clock
	week       bool // the week view, and the Day, Week and Month switch over the month
	nextUp     bool // the next event at the foot of the clock
	weather    bool // the forecast on the calendar's days
	wide       bool // a panel bigger than the Show 5's
}

// lookFor is the switches as c has them, on a panel wide or not.
func lookFor(c config.Calendar, wide bool) calLook {
	return calLook{timeline: !c.DayList, refined: !c.AgendaPlain, monthFull: c.MonthFullOn(wide), icons: !c.NoIcons,
		countdowns: !c.NoCountdowns, week: c.WeekOn(wide), nextUp: !c.NoNextUp, weather: !c.NoWeather, wide: wide}
}

// wideScreen is whether a panel w by h is bigger than the Show 5's 960 by 480: the Show 8, and
// anything larger.
func wideScreen(w, h int) bool { return w > drawnFor || h > drawnFor/2 }

// look is the switches for this renderer's panel.
func (r *renderer) look() calLook { return lookFor(config.Get().Calendar, wideScreen(r.w, r.h)) }

// panelWide is wideScreen for the panel this device has, once the screen is open (Start): what the
// settings screen, the setup page and Home Assistant show for the two switches whose default goes by
// it. Before then, and in a test, it is the Show 5's.
var panelWide atomic.Bool

// deviceLook is the switches on this device's panel.
func deviceLook() calLook { return lookFor(config.Get().Calendar, panelWide.Load()) }

// lookSwitch is one of the switches: its name and the words under it on the screen, its icon in Home
// Assistant, and how it is read and set. id is the settings row's, after "cal_", and Home Assistant's,
// after "calendar_". A choice of two (the month's details) has its two words; a switch has none.
type lookSwitch struct {
	id, label, sub, icon string
	opts                 []string
	get                  func(calLook) bool
	set                  func(bool) error
}

// monthDetailOpts are the month's two choices, the first being on.
var monthDetailOpts = []string{"Full", "Simple"}

var lookSwitches = []lookSwitch{
	{id: "day_timeline", label: "Day timeline", sub: "A day's events on its hours", icon: "mdi:calendar-clock",
		get: func(l calLook) bool { return l.timeline }, set: func(on bool) error { return config.Set().Calendar().DayList(!on) }},
	{id: "coming_up_refined", label: "Coming up, one line each", sub: "With each day's weather", icon: "mdi:format-list-bulleted",
		get: func(l calLook) bool { return l.refined }, set: func(on bool) error { return config.Set().Calendar().AgendaPlain(!on) }},
	{id: "month_details", label: "Month details", sub: "Full: times, icons and weather on the days", icon: "mdi:calendar-month",
		opts: monthDetailOpts,
		get:  func(l calLook) bool { return l.monthFull }, set: func(on bool) error { return config.Set().Calendar().MonthDetails(on) }},
	{id: "event_icons", label: "Event icons", sub: "A small picture for a birthday, trash day, a flight…", icon: "mdi:cake-variant",
		get: func(l calLook) bool { return l.icons }, set: func(on bool) error { return config.Set().Calendar().NoIcons(!on) }},
	{id: "countdowns", label: "Countdowns", sub: "\"in 25 min\" on today's events", icon: "mdi:timer-sand",
		get: func(l calLook) bool { return l.countdowns }, set: func(on bool) error { return config.Set().Calendar().NoCountdowns(!on) }},
	{id: "week_view", label: "Week view", sub: "Seven days on their hours", icon: "mdi:calendar-week",
		get: func(l calLook) bool { return l.week }, set: func(on bool) error { return config.Set().Calendar().Week(on) }},
	{id: "next_up", label: "Next up on the clock", sub: "Today's next event at the foot of the clock", icon: "mdi:calendar-arrow-right",
		get: func(l calLook) bool { return l.nextUp }, set: func(on bool) error { return config.Set().Calendar().NoNextUp(!on) }},
	{id: "weather", label: "Weather in the calendar", sub: "The forecast on each day", icon: "mdi:weather-partly-cloudy",
		get: func(l calLook) bool { return l.weather }, set: func(on bool) error { return config.Set().Calendar().NoWeather(!on) }},
}

// lookRowPrefix begins the settings rows' ids.
const lookRowPrefix = "cal_"

// lookByRow is the switch a settings row is for.
func lookByRow(id string) (lookSwitch, bool) {
	i := slices.IndexFunc(lookSwitches, func(s lookSwitch) bool { return lookRowPrefix+s.id == id })
	if i < 0 {
		return lookSwitch{}, false
	}
	return lookSwitches[i], true
}

// calendarLookRows are the Display card's Calendar rows, once a calendar is shown.
func calendarLookRows() []settingRow {
	if len(config.Get().Calendar.Sources) == 0 {
		return nil
	}
	l := deviceLook()
	rows := []settingRow{{label: "Calendar", kind: ctlHeading}}
	for _, s := range lookSwitches {
		row := settingRow{id: lookRowPrefix + s.id, label: s.label, sub: s.sub, kind: ctlToggle, on: s.get(l)}
		if len(s.opts) > 0 {
			row.kind, row.value = ctlChoice, s.opts[lookOpt(s, l)]
		}
		rows = append(rows, row)
	}
	return rows
}

// lookOpt is which of a choice's two options is in force.
func lookOpt(s lookSwitch, l calLook) int {
	if s.get(l) {
		return 0
	}
	return 1
}

// lookPicker is a choice's list, for the settings screen.
func lookPicker(id string) (pickerView, bool) {
	s, ok := lookByRow(id)
	if !ok || len(s.opts) == 0 {
		return pickerView{}, false
	}
	return pickerView{title: s.label, opts: s.opts, cur: lookOpt(s, deviceLook())}, true
}

// lookRowTap is a tap on one of the rows: a switch turns over, a choice opens its list.
func (d *Display) lookRowTap(id string) bool {
	s, ok := lookByRow(id)
	if !ok {
		return false
	}
	if len(s.opts) > 0 {
		d.openPicker(id)
		return true
	}
	d.setLook(s, !s.get(deviceLook()))
	return true
}

// lookChoose is a choice from a choice's list.
func (d *Display) lookChoose(id string, i int) bool {
	s, ok := lookByRow(id)
	if !ok || len(s.opts) == 0 {
		return false
	}
	if i == 0 || i == 1 {
		d.setLook(s, i == 0)
	}
	return true
}

// setLook sets a switch however it was changed, and shows it in Home Assistant.
func (d *Display) setLook(s lookSwitch, on bool) {
	if err := s.set(on); err != nil {
		slog.Error("saving the calendar's look failed", "setting", s.id, "err", err)
		return
	}
	slog.Info("screen: calendar look", "setting", s.id, "on", on)
	d.lookChanged()
}

// buildLookEntities makes the switches' entities, a switch each and a select for the month.
func (d *Display) buildLookEntities() {
	d.lookEnts = make([]esphome.Entity, len(lookSwitches))
	for i, s := range lookSwitches {
		base := func() esphome.Base {
			return esphome.Base{ObjectID: "calendar_" + s.id, Name: s.label, Icon: s.icon, Category: esphome.CategoryConfig}
		}
		if len(s.opts) > 0 {
			sel := &esphome.Select{Base: base(), Options: s.opts}
			sel.OnCommand = func(v string) {
				if j := slices.Index(s.opts, v); j >= 0 {
					d.setLook(s, j == 0)
				}
			}
			d.lookEnts[i] = sel
			continue
		}
		sw := &esphome.Switch{Base: base()}
		sw.OnCommand = func(on bool) { d.setLook(s, on) }
		d.lookEnts[i] = sw
	}
}

// lookChanged shows the switches as they are in Home Assistant, and draws the screen again.
func (d *Display) lookChanged() {
	l := deviceLook()
	for i, s := range lookSwitches {
		if i >= len(d.lookEnts) {
			break
		}
		switch e := d.lookEnts[i].(type) {
		case *esphome.Select:
			e.Set(s.opts[lookOpt(s, l)])
		case *esphome.Switch:
			e.Set(s.get(l))
		}
	}
	d.wake()
}

// lookChoices are the switches for the setup page (feature/setup's CalendarChoice), each changed the
// way the screen changes it.
func (d *Display) lookChoices() []setup.CalendarChoice {
	var out []setup.CalendarChoice
	for _, s := range lookSwitches {
		opts := s.opts
		if len(opts) == 0 {
			opts = []string{"On", "Off"}
		}
		out = append(out, setup.CalendarChoice{Key: s.id, Label: s.label, Note: s.sub, Options: opts,
			Now:    func() int { return lookOpt(s, deviceLook()) },
			Choose: func(i int) { d.setLook(s, i == 0) }})
	}
	return out
}

// toggleHidden hides a calendar's events on this screen, or shows them again: a chip on the calendar
// page.
func toggleHidden(cal string) {
	hidden := hiddenHere(config.Get().Calendar) // those no longer shown go when the list is next saved
	if i := slices.Index(hidden, cal); i >= 0 {
		hidden = slices.Delete(hidden, i, i+1)
	} else {
		hidden = append(hidden, cal)
	}
	if len(hidden) == 0 {
		hidden = nil
	}
	if err := config.Set().Calendar().Hidden(hidden); err != nil {
		slog.Warn("hiding a calendar failed", "err", err)
		return
	}
	slog.Info("screen: calendars hidden here", "count", len(hidden))
}
