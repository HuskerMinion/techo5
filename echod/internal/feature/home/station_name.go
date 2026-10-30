package home

import (
	"log/slog"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// A station Home Assistant plays here by itself (a voice command, an automation) comes through its
// ffmpeg proxy, which keeps neither the station's address nor its stream's name: the page said "Radio"
// with no logo and no song. media_station names the next play_media as a station, as a tap in a Radio
// Browser list does (listed), so it is looked up and shown as one.

// nameStation names the next stream that starts within listedFor; "" drops a name not yet used.
func (f *Feature) nameStation(name string) {
	name = strings.TrimSpace(name)
	if name != "" {
		slog.Info("radio: the next play_media is a station", "station", name)
	}
	f.mu.Lock()
	f.listed, f.listedAt = name, time.Now()
	f.mu.Unlock()
}

// stationAction is media_station: the station the next play_media is.
func (f *Feature) stationAction() *esphome.Action {
	return &esphome.Action{
		Name: "media_station",
		Args: []esphome.Arg{{Name: "name", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			f.nameStation(c.String("name"))
			return nil, nil
		},
	}
}
