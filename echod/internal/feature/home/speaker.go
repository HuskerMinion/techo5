package home

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Finding this device's own media player in Home Assistant.
//
// Guessing it from the device's name was wrong twice over: the guess used the node name, which has
// dashes, where Home Assistant builds entity ids with underscores — so a device whose name had a
// space in it asked Home Assistant to play a station on an entity nobody had, and nothing answered
// because nothing was there. And an entity id does not follow a rename: Home Assistant keys its
// entities on the MAC address, so a renamed device keeps the entity id it was first given, and any
// guess from the new name is wrong again.
//
// So the registry is asked first, which answers the question that was meant: the media player this
// daemon's own device provides, found by the address the two share. That survives a rename, an entity id
// Home Assistant has prefixed with an area, and a device name that is not what Home Assistant displays.
// The name Home Assistant shows comes second, because it is kept in step with the device's own. The
// guess is the answer of last resort, and says so when it is not an entity anybody has.

// speakerEvery is how long a found entity is trusted before it is looked up again.
const speakerEvery = 6 * time.Hour

var speaker struct {
	sync.Mutex
	id      string
	forName string
	at      time.Time
}

// speakerEntity is this device's media player in Home Assistant.
func speakerEntity() string {
	if s := config.Get().Home.Radio.Speaker; s != "" {
		return s // wired by hand, and then it is nobody else's business
	}
	name := config.Get().Device.Name

	speaker.Lock()
	if speaker.id != "" && speaker.forName == name && time.Since(speaker.at) < speakerEvery {
		id := speaker.id
		speaker.Unlock()
		return id
	}
	speaker.Unlock()

	id := findSpeaker(name)
	speaker.Lock()
	speaker.id, speaker.forName, speaker.at = id, name, time.Now()
	speaker.Unlock()
	return id
}

// findSpeaker asks Home Assistant which media player is this device's: by the device itself first, then
// by the name it shows for it, and the name the entity would have had when neither can be had.
func findSpeaker(name string) string {
	guess := "media_player." + layout.EntitySlug(name) + "_speaker"

	if id := byDevice(ownMAC()); id != "" {
		return found(id, guess)
	}

	players, err := hass.Get().Entities("media_player")
	if err != nil {
		slog.Debug("home: asking Home Assistant for this device's media player", "err", err)
		return guess
	}
	if id := named(players, name); id != "" {
		return found(id, guess)
	}
	// Nothing in either registry is this device's. The guess stands when it is one of the players there;
	// when it is not, every play request through it reaches an entity nobody has — Home Assistant takes
	// the call, logs a warning of its own and returns success — so the device believes it played
	// something and the screen waits on "Starting…". That is worth a line somebody will see.
	if !has(players, guess) {
		slog.Warn("home: no media player in Home Assistant is this device's, so a station cannot play",
			"device", name, "guess", guess, "players", len(players))
	}
	return guess
}

// found says which entity is this device's, when it is not the one it would have guessed.
func found(id, guess string) string {
	if id != guess {
		slog.Info("home: this device's media player in Home Assistant", "entity", id, "guessed", guess)
	}
	return id
}

// registryWait is how long Home Assistant is given to say which media player is this device's. Short on
// purpose: the answer of last resort is still an answer, and a house that cannot say so in a few seconds
// is one to ask again in six hours rather than one to wait on.
const registryWait = 5 * time.Second

// byDevice is the media player this daemon's own device provides, empty when it cannot be asked or there
// is no such device.
func byDevice(mac string) string {
	if mac == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), registryWait)
	defer cancel()
	devices, entities, err := hass.Get().Registries(ctx)
	if err != nil {
		slog.Debug("home: asking Home Assistant for its registries", "err", err)
		return ""
	}
	return mine(devices, entities, mac)
}

// ownMAC is the address the hardware recorded for this device, empty when it cannot be read.
func ownMAC() string {
	mac, err := layout.FactoryMAC()
	if err != nil {
		slog.Debug("home: reading this device's own address", "err", err)
		return ""
	}
	return mac
}

// mine is the media player the device with this address provides, empty when no device carries the address
// or none of them has a media player from ESPHome. It is the answer that survives a rename: the entity id
// itself says nothing about which device provides it.
//
// Every device carrying the address is a candidate rather than the last one. A node that Home Assistant
// added twice — re-added, or added again after a flash — leaves an entry behind with the address still on
// it, so two entries answer to the same MAC and only one of them has the entities. Found on the device this
// was written for: the stale entry won the tie because it came last, the lookup came back empty, and the
// station went to a name nobody has — the screen on "Starting…", which is the bug this whole item is about.
func mine(devices []hass.Device, entities []hass.RegistryEntity, mac string) string {
	if mac == "" {
		return ""
	}
	ours := make(map[string]bool)
	for _, d := range devices {
		for _, c := range d.Connections {
			if len(c) == 2 && c[0] == "mac" && strings.EqualFold(c[1], mac) {
				ours[d.ID] = true
			}
		}
	}
	for _, e := range entities {
		if ours[e.DeviceID] && e.Platform == "esphome" && strings.HasPrefix(e.ID, "media_player.") {
			return e.ID
		}
	}
	return ""
}

// named is the media player whose name Home Assistant shows as this device's.
func named(players []hass.Entity, name string) string {
	want := strings.ToLower(name + " speaker")
	for _, p := range players {
		if strings.ToLower(p.Name) == want {
			return p.ID
		}
	}
	return ""
}

// has is whether that entity id is one of the players there.
func has(players []hass.Entity, id string) bool {
	for _, p := range players {
		if p.ID == id {
			return true
		}
	}
	return false
}
