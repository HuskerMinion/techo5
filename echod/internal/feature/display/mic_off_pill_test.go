//go:build !dot && !spot

package display

import (
	"path/filepath"
	"slices"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// withMuteLight makes the device one with a mute light (light) or without, and Show Microphone off
// on or off, for the length of t.
func withMuteLight(t *testing.T, light, on bool) {
	t.Helper()
	was, hidden := muteLightSaysIt, micOffPillHidden.Load()
	muteLightSaysIt = func() bool { return light }
	micOffPillHidden.Store(!on)
	t.Cleanup(func() { muteLightSaysIt = was; micOffPillHidden.Store(hidden) })
}

// Show Microphone off on, the default, the header says it as it did; off, a Show leaves it to the red
// mute light and still says what the device has lost, in the pill's plain colors (techo5#621). A
// device with no mute light says it whatever the switch says.
func TestShowMicrophoneOffDecidesThePill(t *testing.T) {
	withMuteLight(t, true, true)
	if got := headerLine(scene{muted: true}); got != tr("microphone_off") {
		t.Errorf("switch on: the header says %q", got)
	}

	withMuteLight(t, true, false)
	if got := headerLine(scene{muted: true}); got != "" {
		t.Errorf("switch off, with a mute light: the header says %q", got)
	}
	if got := headerLine(scene{muted: true, linkDown: linkNoWifi}); got != tr("no_wifi") {
		t.Errorf("switch off, Wi-Fi lost: the header says %q", got)
	}
	if (scene{muted: true}).micOffSaid() {
		t.Error("switch off: the pill is still drawn red for the microphone")
	}

	withMuteLight(t, false, false)
	if got := headerLine(scene{muted: true}); got != tr("microphone_off") {
		t.Errorf("no mute light, switch off: the header says %q", got)
	}
}

// The switch is in Home Assistant only where a mute light can say it instead.
func TestShowMicrophoneOffOnlyWithAMuteLight(t *testing.T) {
	d := Get()
	for _, light := range []bool{false, true} {
		withMuteLight(t, light, true)
		has := slices.Contains(d.Entities(), esphome.Entity(d.micOffSw))
		if has != light {
			t.Errorf("mute light %v: Show Microphone off offered %v", light, has)
		}
	}
}

// A device that never saved the switch has it on, so an update changes nobody's header; turned off
// from Home Assistant it is kept, and turned on again it is gone from the config.
func TestShowMicrophoneOffIsOnUntilTurnedOff(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	withMuteLight(t, true, true)
	s := micOffPillSwitch(func() {})
	setMicOffPill(s, !config.Get().Screen.NoMicOffPill)
	if !s.Get() || !micOffPillShown() {
		t.Fatal("a new device: Show Microphone off is not on")
	}
	s.OnCommand(false)
	if !config.Get().Screen.NoMicOffPill || s.Get() || micOffPillShown() {
		t.Fatal("turning it off did not save it or hide the pill")
	}
	s.OnCommand(true)
	if config.Get().Screen.NoMicOffPill || !s.Get() || !micOffPillShown() {
		t.Fatal("turning it on again did not save it or show the pill")
	}
}
