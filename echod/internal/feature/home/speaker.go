package home

import (
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
// So it is asked for rather than guessed: the media player whose name Home Assistant shows is this
// device's, which Home Assistant keeps in step with the device's own name however it was set. The
// guess stays as the answer of last resort, for a device that cannot reach Home Assistant's REST
// API at the moment it is needed.

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

// findSpeaker asks Home Assistant which media player is this device's, by the name it shows for it.
// The guess from the device's name is the answer when it cannot be asked.
func findSpeaker(name string) string {
	guess := "media_player." + layout.EntitySlug(name) + "_speaker"
	players, err := hass.Get().Entities("media_player")
	if err != nil {
		slog.Debug("home: asking Home Assistant for this device's media player", "err", err)
		return guess
	}
	want := strings.ToLower(name + " speaker")
	for _, p := range players {
		if strings.EqualFold(p.Name, name+" Speaker") || strings.ToLower(p.Name) == want {
			if p.ID != guess {
				slog.Info("home: this device's media player in Home Assistant", "entity", p.ID, "guessed", guess)
			}
			return p.ID
		}
	}
	// Nothing named for this device: the guess is as good as anything, and says so in the log if it
	// turns out to be wrong.
	slog.Debug("home: no media player named for this device; using the name it would have",
		"guess", guess, "players", len(players))
	return guess
}
