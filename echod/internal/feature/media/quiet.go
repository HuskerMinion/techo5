package media

import (
	"fmt"
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Quiet hours in Home Assistant, which is the only way to set them on a device with no screen — and
// the Dot, which has none, is the device most likely to be in a bedroom.
//
// What they silence is the noise the device makes about itself: the wake tone, the tones for a turn
// that failed or was dropped, an announcement arriving from another device in the house. Never an
// alarm, a timer, a call, a reply, or anything Home Assistant was told to say, and never anything on
// the ring or the screen. The list lives in config/quiet.go, where the code that reads it is.

// quietOff is the first choice, and how the setting is cleared.
const quietOff = "Off"

// quietWindows are the hours offered. The same set the settings screen offers, in the same order, so
// a house setting this in two places sees one list.
var quietWindows = []string{"22-7", "22-6", "23-7", "0-7", "21-7", "23-8"}

// quietLabel is a window as the list says it: "10 PM – 7 AM", or in whole hours on a 24-hour clock.
func quietLabel(window string) string {
	var from, to int
	if _, err := fmt.Sscanf(window, "%d-%d", &from, &to); err != nil {
		return quietOff
	}
	return hourLabel(from) + " – " + hourLabel(to)
}

// hourLabel is a whole hour, written the way Home Assistant's own cards write times.
func hourLabel(h int) string {
	return time.Date(2000, 1, 1, h, 0, 0, 0, time.UTC).Format("3 PM")
}

func newQuiet() *esphome.Select {
	opts := []string{quietOff}
	for _, w := range quietWindows {
		opts = append(opts, quietLabel(w))
	}
	sel := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "quiet_hours",
			Name:     "Quiet hours",
			Icon:     "mdi:volume-off",
			Category: esphome.CategoryConfig,
		},
		Options: opts,
	}
	sel.OnCommand = func(v string) {
		window := ""
		for _, w := range quietWindows {
			if quietLabel(w) == v {
				window = w
			}
		}
		if err := config.Set().Speaker().QuietHours(window); err != nil {
			slog.Error("saving a setting failed", "setting", "quiet_hours", "err", err)
			return
		}
		sel.Set(v)
		slog.Info("setting changed", "setting", "quiet_hours", "using", window)
	}
	return sel
}

// restoreQuiet puts the saved window back on the select, and takes a window nobody offers — one
// typed into the configuration by hand, which was the only way to set this until now — as Off
// rather than showing a choice that is not in the list.
func restoreQuiet(sel *esphome.Select, c config.Config) {
	want := quietOff
	for _, w := range quietWindows {
		if w == c.Speaker.QuietHours {
			want = quietLabel(w)
		}
	}
	sel.Set(want)
	slog.Info("restored", "what", "quiet_hours", "using", c.Speaker.QuietHours)
}
