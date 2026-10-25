//go:build !dot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
)

// The screen and the room's lights (#179). The screen is a light in Home Assistant (light.<name>_screen),
// on the device, in the device's area, so "lights off" said to Assist in that room put it out with the
// lamps, on every screen there. Assist picks what such a sentence means by the entity's domain and
// area, among the entities exposed to it, and by nothing else: no category, no flag on the entity keeps
// a light that is exposed out of "the lights" (home-assistant/core, helpers/intent.py,
// async_match_targets). What does keep one out is not being exposed, and Home Assistant's own default
// leaves a configuration or diagnostic entity unexposed when it first sees it
// (components/homeassistant/exposed_entities.py, _is_default_exposed).
//
// So there is a setting, Screen ignores room light commands, off at first, so an update changes nothing
// for anyone. On, the light keeps its entity ID, its name and everything it does, and is described as a
// configuration entity. That has a cost the setting's text says: Home Assistant leaves a configuration
// entity out of what an area, a device or a label targets, and out of the dashboards it makes itself, so
// an automation that turns off an area's lights no longer reaches the screen; one that names the light
// by its entity ID still does. Screen power, a switch, is there whichever way the setting is: it puts
// the screen out and lights it again at the brightness it had, and a switch is never one of "the lights"
// (or the fans, the only other domain a room's "off" names), so it can be exposed to Assist and named
// ("turn off the kitchen echo screen power") without a room's lights reaching it. Off, the light is
// described as it always was.
//
// What the device cannot do is take an exposure Home Assistant has already decided: it keeps that
// choice per entity and never looks again when the category changes. A screen that Home Assistant
// already handed to Assist stays handed to it until it is unticked under Settings → Voice assistants →
// Expose, which docs/setup.md says. Unticking it there is the other way to keep "lights off" said to
// Assist away from the screen, with the setting left off and the light where it always was.

// roomLightsIgnored is whether the screen stands apart from the room's lights. Off at first.
func roomLightsIgnored() bool { return config.Get().Screen.IgnoreRoomLights }

// buildRoomLights makes the two switches: Screen power, and the setting. In build, before Restore.
func (d *Display) buildRoomLights() {
	d.power = &esphome.Switch{
		Base: esphome.Base{ObjectID: "screen_power", Name: "Screen power", Icon: "mdi:monitor"},
	}
	// Off and on as the light's own off and on from Home Assistant: the light as it is with only On
	// changed, so the brightness is kept through an off and comes back with the on (command).
	d.power.OnCommand = func(on bool) {
		s := d.light.Get()
		s.On = on
		d.command(s)
	}
	d.roomSw = &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "screen_ignores_room_lights",
			Name:     "Screen ignores room light commands",
			Icon:     "mdi:lightbulb-group-off",
			Category: esphome.CategoryConfig,
		},
	}
	d.roomSw.OnCommand = d.setRoomLightsIgnored
	// What the light says it is changes only while nothing is connected (component.Between), which the
	// setting's reconnect brings about.
	component.Between.Listen(func(struct{}) { d.describeScreen() })
}

// describeScreen puts the setting on the light's description. Only where no connection can be reading
// it: Restore, before anything serves, and component.Between.
func (d *Display) describeScreen() {
	if roomLightsIgnored() {
		d.light.Category = esphome.CategoryConfig
	} else {
		d.light.Category = esphome.CategoryNone
	}
}

// reconnectWhenIdle drops Home Assistant's connection once the device is idle, as the setup page does
// for a wake word (setup, reconnect.go); a test has one of its own.
var reconnectWhenIdle = setup.ReconnectWhenIdle

// setRoomLightsIgnored saves the setting and has Home Assistant told: a reconnect, since what an entity
// is reaches Home Assistant only when it connects. A moment without Home Assistant, which is why it is
// asked for only on a real change, one that was saved, and waits until the device is idle: no request
// being answered, no call, no announcement being read. Anything else only puts the switch back where the
// setting is, since Home Assistant may have moved it. A save that failed is taken back
// (config.ScreenWriter.IgnoreRoomLights), so that is where it was.
//
// The remote API and settings sharing may set it like any other screen setting: whoever sets it over and
// over gets no more than one reconnect each reconnectGap (setup, reconnect.go), joined with any already
// waiting, and only once the device is idle.
func (d *Display) setRoomLightsIgnored(ignore bool) {
	was := roomLightsIgnored()
	if ignore == was {
		d.roomSw.Set(was)
		return
	}
	if err := config.Set().Screen().IgnoreRoomLights(ignore); err != nil {
		slog.Error("saving a setting failed", "setting", "screen_ignores_room_lights", "err", err)
		d.roomSw.Set(roomLightsIgnored())
		return
	}
	slog.Info("setting changed", "setting", "screen_ignores_room_lights", "using", ignore)
	d.roomSw.Set(ignore)
	reconnectWhenIdle()
	d.wake()
}
