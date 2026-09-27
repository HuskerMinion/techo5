package home

import (
	"testing"
	"time"
)

// The action's sound argument is optional in both directions: an automation that wants the doorbell
// heard asks for it, one that wants a view quiet refuses it, and anything else — an absent argument
// included — is the device's own setting answering. Getting this wrong is silent: a camera that
// plays its audio when nobody asked, or one that refuses to when they did.
func TestSoundAsked(t *testing.T) {
	cases := []struct {
		arg     string
		setting bool
		want    bool
	}{
		{"", true, true},
		{"", false, false},
		{"on", false, true},
		{"ON", false, true},
		{" on ", false, true},
		{"off", true, false},
		{"Off", true, false},
		{"true", false, true},
		{"false", true, false},
		{"1", false, true},
		{"0", true, false},
		{"sometimes", true, true}, // not a word this understands: the setting answers
		{"sometimes", false, false},
	}
	for _, c := range cases {
		if got := soundAsked(c.arg, c.setting); got != c.want {
			t.Errorf("soundAsked(%q, setting=%v) = %v, want %v", c.arg, c.setting, got, c.want)
		}
	}
}

// The end of a view must take its own sound off the speaker and nothing else. The player holds one
// stream at a time, so the sound is this view's only while what is playing is still the stream that
// answered it: somebody's music started in between is theirs, and the camera closing must not stop it.
func TestCameraSoundStopsOnlyItsOwnTrack(t *testing.T) {
	const (
		deck  = "camera.deck"
		door  = "camera.door"
		mine  = "http://homeassistant.local:8123/api/esphome/ffmpeg_proxy/abc/one.wav"
		mine2 = "http://homeassistant.local:8123/api/esphome/ffmpeg_proxy/abc/two.wav"
		music = "http://homeassistant.local:8123/api/esphome/ffmpeg_proxy/abc/radio.wav"
	)

	cases := []struct {
		name     string
		entity   string
		sound    string // camSound: whose audio was started
		answered string // camSoundURL: the stream that answered it, empty if none did
		playing  string // url: what the player is playing now
		up       bool   // the view is still up
		wantUp   bool
		wantOurs bool
	}{
		{name: "up, its own track", entity: deck, sound: deck, answered: mine, playing: mine, up: true,
			wantUp: true, wantOurs: true},
		{name: "up, music started since", entity: deck, sound: deck, answered: mine, playing: music, up: true,
			wantUp: true, wantOurs: false},
		{name: "up, the stream has not answered yet", entity: deck, sound: deck, answered: "", playing: music, up: true,
			wantUp: true, wantOurs: false},
		{name: "down, its own track", entity: deck, sound: deck, answered: mine, playing: mine,
			wantUp: false, wantOurs: true},
		{name: "down, music started since", entity: deck, sound: deck, answered: mine, playing: music,
			wantUp: false, wantOurs: false},
		{name: "down, the stream never answered", entity: deck, sound: deck, answered: "",
			wantUp: false, wantOurs: false},
		{name: "another camera's sound is playing", entity: deck, sound: door, answered: mine, playing: mine,
			wantUp: false, wantOurs: false},
		{name: "a view asked for with no sound", entity: deck, sound: "", answered: "", playing: mine,
			wantUp: false, wantOurs: false},
		{name: "a different camera's track answered this view", entity: deck, sound: deck, answered: mine, playing: mine2,
			wantUp: false, wantOurs: false},
	}

	for _, c := range cases {
		f := &Feature{camSound: c.sound, camSoundURL: c.answered}
		f.url = c.playing
		f.cam = CameraView{Entity: c.entity, span: time.Minute}
		if c.up {
			f.cam.Until = time.Now().Add(time.Minute)
		}

		up, ours := f.cameraSoundEnds(c.entity)
		if up != c.wantUp || ours != c.wantOurs {
			t.Errorf("%s: cameraSoundEnds = (up %v, ours %v), want (%v, %v)",
				c.name, up, ours, c.wantUp, c.wantOurs)
		}
		// Whatever was decided: a view's own sound is not left behind for the next one to stop, and
		// anybody else's is not touched.
		wantSound := c.sound
		if !c.wantUp && c.sound == c.entity {
			wantSound = ""
		}
		if f.camSound != wantSound {
			t.Errorf("%s: camSound is %q after the view ended, want %q", c.name, f.camSound, wantSound)
		}
	}
}

// A view still up keeps its sound: the check that decides whether to stop it must not stop anything
// while the camera is on screen, however long it is left there.
func TestCameraSoundStaysWhileTheViewDoes(t *testing.T) {
	f := &Feature{camSound: "camera.deck", camSoundURL: "http://ha/one.wav"}
	f.url = "http://ha/one.wav"
	f.cam = CameraView{Entity: "camera.deck", Until: time.Now().Add(time.Hour)}

	for i := 0; i < 3; i++ {
		up, ours := f.cameraSoundEnds("camera.deck")
		if !up {
			t.Fatalf("the view was taken as over while it has an hour to run")
		}
		if !ours {
			t.Fatalf("the sound was taken as not ours while nothing else has played")
		}
	}
	if f.camSound != "camera.deck" {
		t.Fatalf("the view's own sound was forgotten while the view was up: %q", f.camSound)
	}
}
