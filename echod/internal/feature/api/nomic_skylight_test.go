//go:build !dot && !spot && !panel

package api

import (
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/layout"

	// The features that are about listening, so that their entities are in the registry here.
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/activity"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/clap"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/detect"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/microphone"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/nearest"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/noise"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/recording"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/room"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
)

// skylight makes this a board without microphones for the test.
func skylight(t *testing.T) {
	was := layout.Board
	t.Cleanup(func() { layout.Board = was })
	layout.Board = "skylight"
	if layout.HasMicrophone() {
		t.Fatal("the Skylight board still has a microphone")
	}
}

// On a board without microphones, nothing of the features that only listen is listed, whatever its
// name: a new entity in one of them goes with the rest without being named in nomic.go. And nothing on
// the microphone's or an assistant's page is listed from any feature, nor are those pages.
func TestNoListeningEntityOnABoardWithoutMicrophones(t *testing.T) {
	skylight(t)
	onlyListen := map[string]bool{
		"activity": true, "clap gesture": true, "wake": true, "microphone settings": true,
		"microphone mute": true, "nearest device": true, "ambient noise": true, "recording": true,
		"room reaction": true, "wake word settings": true,
	}
	found := map[string]bool{}
	for _, c := range component.Default().All() {
		ec, ok := c.(component.Entities)
		if !ok || !onlyListen[c.Name()] {
			continue
		}
		found[c.Name()] = true
		for _, e := range heard(ec.Entities()) {
			t.Errorf("%s: %q is listed on a board without microphones", c.Name(), e.Object())
		}
	}
	for name := range onlyListen {
		if !found[name] {
			t.Errorf("no component %q with entities: renamed?", name)
		}
	}
	all := component.Default().Entities()
	for _, e := range heard(all) {
		if listeningDevice(deviceOf(e)) {
			t.Errorf("%q is on a listening page (%d) and still listed", e.Object(), deviceOf(e))
		}
	}
	for _, d := range subDevices("Kitchen") {
		if listeningDevice(d.ID) {
			t.Errorf("the %q page is offered on a board without microphones", d.Name)
		}
	}
	// What a board with microphones lists is all of it, pages included.
	layout.Board = "cronos"
	if got := heard(all); len(got) != len(all) {
		t.Errorf("a board with microphones lists %d of %d entities", len(got), len(all))
	}
	mic := false
	for _, d := range subDevices("Kitchen") {
		mic = mic || d.ID == component.DeviceMicrophone
	}
	if !mic {
		t.Error("a board with microphones has no microphone page")
	}
}

// An entity goes by its sub-device too, whatever it is called.
func TestListeningPagesAreDropped(t *testing.T) {
	sw := func(id string, dev uint32) esphome.Entity {
		return &esphome.Switch{Base: esphome.Base{ObjectID: id, Name: id, DeviceID: dev}}
	}
	in := []esphome.Entity{
		sw("a_mic_thing", component.DeviceMicrophone), sw("an_assistant_thing", component.AssistantDevice(1)),
		sw("a_speaker_thing", component.DevicePlayback), sw("screen", 0),
		&esphome.Sensor{Base: esphome.Base{ObjectID: "a_reading", DeviceID: component.AssistantDevice(0)}},
	}
	var got []string
	for _, e := range dropListening(in) {
		got = append(got, e.Object())
	}
	if len(got) != 2 || got[0] != "a_speaker_thing" || got[1] != "screen" {
		t.Fatalf("kept %v, want [a_speaker_thing screen]", got)
	}
}
