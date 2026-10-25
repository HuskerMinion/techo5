//go:build !dot

package display

import (
	"os"
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

// The screen stands with the room's lights until asked otherwise (#179), so an update changes nothing:
// on, its light is described as a configuration entity, which Home Assistant does not hand to Assist when
// it first sees it, and the setting changes that only while nothing is connected. The light keeps its
// object ID whichever way, so its entity ID in Home Assistant, and the automations that name it, stay as
// they were.
func TestTheScreenStandsWithTheRoomLightsUntilAsked(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	reconnects := 0
	was := reconnectWhenIdle
	reconnectWhenIdle = func() { reconnects++ }
	t.Cleanup(func() { reconnectWhenIdle = was })
	d := roomLightsDisplay()
	d.roomSw.Set(config.Get().Screen.IgnoreRoomLights)
	d.describeScreen()
	if d.roomSw.Get() || d.light.Category != esphome.CategoryNone {
		t.Fatalf("a new device: setting %v, light category %v; want off, and the light as it always was", d.roomSw.Get(), d.light.Category)
	}

	d.roomSw.OnCommand(true)
	if !config.Get().Screen.IgnoreRoomLights || !d.roomSw.Get() {
		t.Fatal("turning the setting on did not save it")
	}
	if reconnects != 1 {
		t.Errorf("turning it on asked for %d reconnects; want one, once the device is idle", reconnects)
	}
	if d.light.Category != esphome.CategoryNone {
		t.Fatal("the light's description changed while a connection could have been reading it")
	}
	component.Between.Emit(struct{}{})
	if d.light.Category != esphome.CategoryConfig {
		t.Errorf("light category %v between connections with the setting on; want a configuration entity", d.light.Category)
	}
	if d.light.ObjectID != "screen" {
		t.Errorf("the light's object ID is %q; Home Assistant would take it for a new entity", d.light.ObjectID)
	}

	d.roomSw.OnCommand(false)
	component.Between.Emit(struct{}{})
	if config.Get().Screen.IgnoreRoomLights || d.light.Category != esphome.CategoryNone {
		t.Error("turning the setting back off did not put the light back among the room's lights")
	}
	if reconnects != 2 {
		t.Errorf("%d reconnects after turning it on and off; want two", reconnects)
	}
}

// The setting asked for again as it already is, from Home Assistant or the sheet, does not drop Home
// Assistant's connection for nothing, and a setting that could not be saved neither reconnects nor leaves
// the switch saying what was not kept. Either way the switch is put back where the setting is.
func TestTheRoomLightsSettingReconnectsOnlyOnARealChange(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	reconnects := 0
	was := reconnectWhenIdle
	reconnectWhenIdle = func() { reconnects++ }
	t.Cleanup(func() { reconnectWhenIdle = was })
	d := roomLightsDisplay()
	d.roomSw.Set(true) // as Home Assistant may have left it, with the setting off
	d.roomSw.OnCommand(false)
	if reconnects != 0 || d.roomSw.Get() {
		t.Errorf("the setting asked for as it is: %d reconnects, switch %v; want none, and off", reconnects, d.roomSw.Get())
	}

	// A file that is new, so readable, in a folder that a plain file then takes the place of: it cannot
	// be written, as root or not.
	gone := filepath.Join(t.TempDir(), "gone")
	config.Use(filepath.Join(gone, "state.json"))
	if err := os.WriteFile(gone, nil, 0600); err != nil {
		t.Fatal(err)
	}
	d.roomSw.OnCommand(true)
	if reconnects != 0 || d.roomSw.Get() || config.Get().Screen.IgnoreRoomLights {
		t.Errorf("a setting that was not saved: %d reconnects, switch %v; want none, and off", reconnects, d.roomSw.Get())
	}
}

// roomLightsDisplay is a Display with only its light and the two switches.
func roomLightsDisplay() *Display {
	d := &Display{
		poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"},
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen", Icon: "mdi:monitor"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
	}
	d.buildRoomLights()
	return d
}

// Screen power is the screen's own off and on: off puts the light out with it, and on lights it again at
// the brightness it was left at, not at some default.
func TestScreenPowerIsTheLightsOffAndOn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := roomLightsDisplay()
	d.apply(true, 37, true)
	if !d.power.Get() {
		t.Fatal("Screen power is off with the screen lit")
	}

	d.power.OnCommand(false)
	if d.light.Get().On || d.power.Get() {
		t.Fatalf("after Screen power off: light on %v, switch %v", d.light.Get().On, d.power.Get())
	}
	d.power.OnCommand(true)
	got := d.light.Get()
	if !got.On || !d.power.Get() {
		t.Fatalf("after Screen power on: light on %v, switch %v", got.On, d.power.Get())
	}
	if pct := int(got.Brightness*100 + 0.5); pct != 37 {
		t.Errorf("the screen came back at %d%%; want the 37%% it was left at", pct)
	}
}
