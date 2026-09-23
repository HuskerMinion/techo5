//go:build !dot

package display

import (
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// stripOptions are the setting's choices, and stripSeconds what each means: 0 keeps the full page.
var (
	stripOptions = []string{"Full page", "Strip after 10 s", "Strip after 30 s", "Strip at once"}
	stripSeconds = []int{0, 10, 30, 1}
)

// stripFull is how long a tap on the strip's song brings the full page back for.
const stripFull = 30 * time.Second

// stripIndex is the setting's place in stripOptions.
func stripIndex() int {
	n := config.Get().Screen.MusicStrip
	for i, s := range stripSeconds {
		if s == n {
			return i
		}
	}
	return 0
}

// stripSelect is the setting in Home Assistant; the screen's own row sets the same thing.
func stripSelect(wake func()) *esphome.Select {
	s := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "screen_now_playing",
			Name:     "Now playing",
			Icon:     "mdi:music-box-outline",
			Category: esphome.CategoryConfig,
		},
		Options: stripOptions,
	}
	s.OnCommand = func(v string) {
		for i, o := range stripOptions {
			if o == v {
				setStrip(s, i)
				wake()
				return
			}
		}
	}
	return s
}

// setStrip saves a choice and shows it in Home Assistant.
func setStrip(s *esphome.Select, i int) {
	if err := config.Set().Screen().MusicStrip(stripSeconds[i]); err != nil {
		slog.Error("saving the now playing setting failed", "err", err)
		return
	}
	if s != nil {
		s.Set(stripOptions[i])
	}
}

// stripOptionText is the choice as the settings row shows it.
func stripOptionText() string { return stripOptions[stripIndex()] }

// stripChoices and stripIndexShared are the settings list's.
func stripChoices() []string { return stripOptions }
func stripIndexShared() int  { return stripIndex() }
