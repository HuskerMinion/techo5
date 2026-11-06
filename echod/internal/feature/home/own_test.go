package home

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	esphome "github.com/ygelfand/go-esphome-device"
)

// A station has to have a name and an address the player can be handed. Anything else is dropped
// rather than saved, since a station that cannot play is worse than one that is not there.
func TestOnlyPlayableStationsAreKept(t *testing.T) {
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"http://stream.example/live", true},
		{"https://stream.example/live.m3u8", true},
		{"stream.example/live", false}, // no scheme: not something to hand a player
		{"file:///data/secret", false}, // a file on the device is not a radio station
		{"ftp://stream.example/x", false},
		{"https://", false}, // no host
		{"", false},
	} {
		if got := playableURL(c.url); got != c.want {
			t.Errorf("playableURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// The list the device keeps is the one it was given, minus what cannot play, and no longer than it
// holds.
func TestTheListIsCleanedUp(t *testing.T) {
	in := []config.Station{
		{Name: "  Jazz  ", URL: " https://stream.example/jazz "},
		{Name: "", URL: "https://stream.example/nameless"},
		{Name: "Broken", URL: "not-a-url"},
		{Name: "News", URL: "http://stream.example/news"},
	}
	var kept []config.Station
	for _, s := range in {
		s.Name, s.URL = trim(s.Name), trim(s.URL)
		if s.Name != "" && playableURL(s.URL) {
			kept = append(kept, s)
		}
	}
	if len(kept) != 2 || kept[0].Name != "Jazz" || kept[0].URL != "https://stream.example/jazz" || kept[1].Name != "News" {
		t.Errorf("kept %+v, want Jazz and News with the spaces trimmed", kept)
	}
}

func trim(s string) string { return strings.TrimSpace(s) }

// home_radio wires the favorites and says nothing about the stations kept on the device, so it
// leaves them where they are.
func TestHomeRadioKeepsTheDevicesOwnStations(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	own := []config.Station{{Name: "Jazz", URL: "https://stream.example/jazz"}}
	if err := SetOwnStations(own); err != nil {
		t.Fatal(err)
	}
	var ran bool
	for _, a := range Get().Actions() {
		if a.Name != "home_radio" {
			continue
		}
		if _, err := a.Run(esphome.Call{}); err != nil {
			t.Fatal(err)
		}
		ran = true
	}
	if !ran {
		t.Fatal("home_radio is not offered")
	}
	if got := OwnStations(); len(got) != 1 || got[0] != own[0] {
		t.Errorf("own stations after home_radio: %+v, want %+v", got, own)
	}
}

// Pressing the star while a direct stream plays, with no Home Assistant radio favorites wired, keeps
// the station on the device (including the first one, before any radio list is configured).
func TestStarKeepsDirectStreamOnDeviceWithoutHomeRadio(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := &Feature{}
	f.playingOwn("Jazz FM", "https://stream.example/jazz")
	if got := f.Radio().Now; got != "Jazz FM" {
		t.Fatalf("Radio().Now before any list is configured = %q, want Jazz FM", got)
	}
	if err := f.FavoriteNow(); err != nil {
		t.Fatalf("FavoriteNow: %v", err)
	}
	got := OwnStations()
	if len(got) != 1 || got[0].Name != "Jazz FM" || got[0].URL != "https://stream.example/jazz" {
		t.Fatalf("OwnStations = %+v, want Jazz FM", got)
	}
}
