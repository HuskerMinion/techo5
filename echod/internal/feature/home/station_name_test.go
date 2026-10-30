package home

import (
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

const proxied = "http://ha.invalid/api/esphome/ffmpeg_proxy/x/a.wav"

func stationRig(t *testing.T) *Feature {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	return &Feature{}
}

func nameOfStream(f *Feature) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.urlName
}

// media_station names the next play_media, as a tapped station does, and only that one.
func TestMediaStationNamesTheNextStream(t *testing.T) {
	f := stationRig(t)
	f.nameStation(" SWR3 ")

	f.played(proxied)
	if got := nameOfStream(f); got != "SWR3" {
		t.Fatalf("named %q, want SWR3", got)
	}
	f.played("http://ha.invalid/api/esphome/ffmpeg_proxy/x/b.wav")
	if got := nameOfStream(f); got == "SWR3" {
		t.Fatal("the stream after it is named SWR3 too")
	}
}

// A name whose play_media did not come within listedFor names nothing.
func TestMediaStationExpires(t *testing.T) {
	f := stationRig(t)
	f.nameStation("SWR3")
	f.mu.Lock()
	f.listedAt = time.Now().Add(-listedFor - time.Second)
	f.mu.Unlock()

	f.played(proxied)
	if got := nameOfStream(f); got == "SWR3" {
		t.Fatal("an old name named the stream")
	}
}

// media_station with an empty name drops one given before.
func TestAnEmptyMediaStationNamesNothing(t *testing.T) {
	f := stationRig(t)
	f.nameStation("SWR3")
	if _, err := f.stationAction().Run(esphome.Call{}); err != nil { // name ""
		t.Fatal(err)
	}

	f.played(proxied)
	if got := nameOfStream(f); got == "SWR3" {
		t.Fatal("a dropped name named the stream")
	}
}
