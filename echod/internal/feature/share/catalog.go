package share

import (
	"errors"
	"math"
	"slices"
	"strconv"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// The settings that may be shared: Home Assistant's configuration switches, numbers and choices, by their
// object IDs, each changed the way Home Assistant changes it, so a shared change takes effect at once and
// is saved like any other.
//
// It is a list, not a rule: a setting added to the device is not shared until somebody decides it should
// be. What is never on it is what a settings file never carries either (config.KeepOwn and the secrets):
// what opens a door on the network, uses the microphones or the camera for something new, or is this
// device's own, plus where its voice, words or photos go, and nothing the remote API holds read-only. A
// test (component/all) changes every setting on the list and checks it touches none of those.

// group is a section of the settings, as the setup page lists them.
type group struct {
	Name string
	IDs  []string
}

// What is left off, beyond the rule above, and why. Anything about the microphones: what they hear (gain,
// echo canceling, noise reduction, leveling), how readily and how long they listen (wake word thresholds,
// the confirming threshold, follow-ups, how long one listens, the end of speech, the room's
// sensitivity), and how readily a stop word stops something (stop and cancel off, or stopping at every
// sound), as the remote API holds them. The signs that the device is listening or muted (wake tones and
// effects, the follow-up tone, the mute light's brightness), which another device must never be able to
// turn off, and quiet hours, which silence the wake tone. How loud an alarm rings, which another device
// could turn down to nothing; severe weather alerts, which it could turn off. Do not disturb for
// the intercom, which blocks calls. Which photos and calendar events a screen shows (a shared room's
// screen showing one person's). Which outside service is told where the house is or what is playing (the
// weather, the rain map, lyrics). The test in component/all keeps them off (neverShared).
var groups = []group{
	{"Speaker", []string{"bass", "treble", "speaker_eq", "night_volume", "home_assistant_sounds",
		"media_duck_level", "duck_on_near_miss", "bluetooth_sets_volume"}},
	{"Voice: first wake word", []string{"thinking_effect_1", "replying_effect_1", "reply_delivery_1",
		"reply_buffer_1", "max_think_1"}},
	{"Voice: second wake word", []string{"thinking_effect_2", "replying_effect_2", "reply_delivery_2",
		"reply_buffer_2", "max_think_2"}},
	{"Alarms", []string{"alarm_sound", "alarm_snooze_length"}},
	{"Screen", []string{"screen_skin", "screen_theme_mode", "screen_theme", "screen_light_theme", "screen_font", "screen_language", "screen_clock_style", "screen_clock_format",
		"screen_clock_position", "screen_date_color", "screen_weather_position", "screen_clock_style_swipe", "screen_volume_swipe", "screen_clock_tap",
		"screen_turn_style", "screen_timer_style", "screen_answer_time", "screen_camera_time", "screen_call_button", "screen_now_playing",
		"screen_weather_animation", "screen_auto_brightness", "screen_auto_brightness_dimmest",
		"screen_ignores_room_lights"}},
	{"Night", []string{"screen_at_night", "screen_night_start", "screen_night_end", "screen_night_hours",
		"screen_night_clock_style", "screen_night_light_level", "screen_night_tap_lifts"}},
	{"Photos", []string{"slideshow_interval", "slideshow_shuffle", "slideshow_subfolders", "slideshow_whole_photo",
		"slideshow_pairs", "slideshow_caption", "slideshow_weather_art", "slideshow_screensaver_idle",
		"slideshow_screensaver_overlay"}},
	{"Calendar", []string{"calendar_opens_on", "calendar_popup_before", "calendar_popup_all_day", "calendar_popup_chime",
		"calendar_day_timeline", "calendar_coming_up_refined", "calendar_month_details", "calendar_event_icons", "calendar_countdowns", "calendar_week_view", "calendar_next_up", "calendar_weather"}},
	{"Dashboard", []string{"screen_dashboard_idle", "screen_dashboard_kiosk", "screen_dashboard_return",
		"screen_dashboard_tiles"}},
	{"Calls and cameras", []string{"phone_ring_sound", "camera_sound"}},
}

// shareable is every setting on the list.
var shareable = func() map[string]bool {
	m := map[string]bool{}
	for _, g := range groups {
		for _, id := range g.IDs {
			m[id] = true
		}
	}
	return m
}()

// Shareable is whether a setting may be shared at all.
func Shareable(id string) bool { return shareable[id] }

// IDs is every setting that may be shared, in the setup page's order.
func IDs() []string {
	var out []string
	for _, g := range groups {
		out = append(out, g.IDs...)
	}
	return out
}

// entities finds the entity for a setting on this device; false when this device has none (a Dot has no
// screen, say).
var entities = func(id string) (esphome.Entity, bool) {
	for _, e := range component.Default().Entities() {
		if objectID(e) == id {
			return e, true
		}
	}
	return nil, false
}

func objectID(e esphome.Entity) string {
	switch v := e.(type) {
	case *esphome.Switch:
		return v.ObjectID
	case *esphome.Number:
		return v.ObjectID
	case *esphome.Select:
		return v.ObjectID
	}
	return ""
}

// name is a setting's name as Home Assistant shows it, or its object ID where this device has none.
func name(id string) string {
	e, ok := entities(id)
	if !ok {
		return id
	}
	switch v := e.(type) {
	case *esphome.Switch:
		return v.Name
	case *esphome.Number:
		return v.Name
	case *esphome.Select:
		return v.Name
	}
	return id
}

// valueOf is a setting's value as it travels: on or off, a number, or the option chosen.
func valueOf(e esphome.Entity) (string, bool) {
	switch v := e.(type) {
	case *esphome.Switch:
		if v.Get() {
			return "on", true
		}
		return "off", true
	case *esphome.Number:
		return strconv.FormatFloat(float64(v.Get()), 'g', -1, 32), true
	case *esphome.Select:
		return v.Get(), true
	}
	return "", false
}

var errValue = errors.New("not a value this setting takes")

// apply sets a setting as Home Assistant would, through its own command.
func apply(e esphome.Entity, value string) error {
	switch v := e.(type) {
	case *esphome.Switch:
		var on bool
		switch value {
		case "on":
			on = true
		case "off":
		default:
			return errValue
		}
		if v.OnCommand != nil {
			v.OnCommand(on)
		} else {
			v.Set(on)
		}
		return nil
	case *esphome.Number:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < v.Min || f > v.Max {
			return errValue
		}
		if v.OnCommand != nil {
			v.OnCommand(float32(f))
		} else {
			v.Set(float32(f))
		}
		return nil
	case *esphome.Select:
		if !slices.Contains(v.Options, value) {
			return errValue
		}
		if v.OnCommand != nil {
			v.OnCommand(value)
		} else {
			v.Set(value)
		}
		return nil
	}
	return errValue
}

// Setting is one setting as the setup page lists it.
type Setting struct {
	ID, Name string
}

// Section is a group of settings as the setup page lists them.
type Section struct {
	Name     string
	Settings []Setting
}

// Sections is the settings that can be shared that this device has, in groups.
func Sections() []Section {
	var out []Section
	for _, g := range groups {
		var s Section
		s.Name = g.Name
		for _, id := range g.IDs {
			if _, ok := entities(id); ok {
				s.Settings = append(s.Settings, Setting{ID: id, Name: name(id)})
			}
		}
		if len(s.Settings) > 0 {
			out = append(out, s)
		}
	}
	return out
}
