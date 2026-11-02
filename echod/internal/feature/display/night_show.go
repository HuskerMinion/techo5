//go:build !dot && !spot

package display

import (
	"errors"
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/screen"
)

// The Show's own parts of the night (night_select.go has what the Spot shares): its actions, the pages
// Home Assistant puts up over the night, and the night light's scale on its panels.

// nightHoursSet is the night hours set; empty is no night.
func nightHoursSet() string { return config.Get().Screen.Night }

// nightNever is the night hours that are no night.
const nightNever = ""

// Actions: screen_night_hours sets the night to any start and end, "19:00" and "09:30"; both empty is
// no night. screen_weekend_night_hours sets the weekend's the same way; both empty is the same as every
// other night. The rest put a page up from an automation: the calendar, the forecast, the rain map.
func (d *Display) Actions() []*esphome.Action {
	return []*esphome.Action{{
		Name: "screen_night_hours",
		Args: []esphome.Arg{{Name: "start", Type: esphome.ArgString}, {Name: "end", Type: esphome.ArgString}},
		Run:  func(c esphome.Call) (any, error) { return nil, d.setNightHours(c.String("start"), c.String("end")) },
	}, {
		Name: "screen_weekend_night_hours",
		Args: []esphome.Arg{{Name: "start", Type: esphome.ArgString}, {Name: "end", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			return nil, d.setWeekendNightHours(c.String("start"), c.String("end"))
		},
	}, {
		Name: "calendar_popup_sources",
		Args: []esphome.Arg{{Name: "calendars", Type: esphome.ArgString}},
		Run:  func(c esphome.Call) (any, error) { return nil, setPopupCalendars(c.String("calendars")) },
	}, {
		Name: "calendar_show",
		Run: func(esphome.Call) (any, error) {
			if !d.OpenCalendar() {
				return nil, errors.New("calendar: this device shows no calendar; choose one with calendar_sources")
			}
			d.askedAtNight(time.Now())
			return nil, nil
		},
	}, {
		Name: "todo_show",
		Run: func(esphome.Call) (any, error) {
			if !d.OpenTodo() {
				return nil, errors.New("todo: this device shows no to-do list; choose one with todo_lists")
			}
			d.askedAtNight(time.Now())
			return nil, nil
		},
	}, {
		Name: "weather_show",
		Run:  func(esphome.Call) (any, error) { return nil, d.weatherShown(false) },
	}, {
		Name: "radar_show",
		Run:  func(esphome.Call) (any, error) { return nil, d.weatherShown(true) },
	}}
}

// weatherShown is the weather_show and radar_show actions: the forecast or the rain map, as a tap on
// the weather and the page's Radar button do. Not on a device set to no weather: its forecast page
// would be empty, and somebody who turned the weather off did not ask for the rain map either.
func (d *Display) weatherShown(radar bool) error {
	if config.Get().Home.WeatherEntity() == "" {
		return errors.New("weather: this device shows no weather; choose a source with home_weather")
	}
	d.ShowWeather(radar)
	d.askedAtNight(time.Now())
	return nil
}

// askedAtNight shows a page Home Assistant asked for over the night, as a video asked for is shown:
// a screen the night put out is lit, at the night light's level when one is set and at the night's
// half brightness when not (relight), and the red night clock gives way to the page. The night comes
// back once the page is gone. Somebody set this up to be seen: a storm's rain map, the day's
// calendar at the alarm. By day, or on a screen somebody has already brought up, there is nothing to
// lift. A screen somebody turned off stays off: the page opens under it all the same, as by day, but
// lighting it would leave it lit at the night light's level all night.
func (d *Display) askedAtNight(now time.Time) {
	if !nightNow(now) {
		return
	}
	d.mu.Lock()
	on := d.on
	if (on && !d.nightGlow) || (!on && !d.nightDark) {
		d.mu.Unlock()
		return
	}
	d.askedNight = true
	if !on {
		d.nightGlow, d.nightDark = config.Get().Screen.NightLight, false
	}
	d.mu.Unlock()
	if !on {
		slog.Info("screen: lit at night for a page Home Assistant asked for")
		d.apply(true, d.ceilingOrDefault(), false)
	}
	d.wake()
}

// askedUp is whether a page Home Assistant asked for at night is still up: the hold lasts while the
// page does, the Radar button or a tap on the calendar keeping it up keeping the hold too, and ends
// the first time the page is found closed, so a page put up later is not held for it. Called with
// d.mu held.
func (d *Display) askedUp(now time.Time) bool {
	if !d.askedNight {
		return false
	}
	if now.Before(d.weatherUntil) || now.Before(d.calUntil) || (d.drawer && d.drawerTab == drawerTodo) {
		return true
	}
	d.askedNight = false
	return false
}

// glowSteps is the night light's scale, 1 to 10, as backlight out of screen.BacklightMax: tight at the
// bottom, where one step is the difference between a glow and a light in a dark room.
var glowSteps = [...]int{1, 2, 3, 4, 6, 8, 10, 13, 16, 20}

// glowSetting is the night light's brightness on its 1 to 10 scale: the one set, or the panel's own.
// The Show 5 starts where the night light began; the Show 8's panel lights a dark room far more at the
// same backlight, so it starts lower. The Skylight on its kernel that dims under stock's floor starts at the
// top, about where that floor kept it before (screen.skylightRaw); on the kernel before, as the Show 8 does.
func (d *Display) glowSetting() int {
	d.mu.Lock()
	wide := d.wide
	d.mu.Unlock()
	return glowFor(wide)
}

// glowFor is glowSetting for a panel already known, for callers holding mu.
func glowFor(wide bool) int {
	if v := config.Get().Screen.NightLightLevel; v > 0 {
		return v
	}
	if screen.SkylightUnderFloor() {
		return len(glowSteps)
	}
	if wide {
		return 3
	}
	return 6
}

// hasGlowRow: the Show's night light brightness is set in Home Assistant only.
const hasGlowRow = false

// glowLevelSet and stepGlow are the Spot's brightness row, which the Show does not have.
func glowLevelSet() int { return glowFor(false) }

func (d *Display) stepGlow(int) {}

// nightClockSoon: the Show takes its night light up once nothing has happened for nightIdle, Night
// light chosen or not.
func (d *Display) nightClockSoon() {}

// glowBacklight is the backlight the night light shows at on the panel. Wants mu, for wide.
func (d *Display) glowBacklight() int {
	return glowSteps[min(max(glowFor(d.wide), 1), len(glowSteps))-1]
}
