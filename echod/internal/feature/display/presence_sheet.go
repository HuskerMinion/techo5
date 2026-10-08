//go:build !dot

package display

import (
	"fmt"
	"slices"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

// awayChoices are the screen's waits for an empty room the settings offer, in minutes; 0 is never.
var awayChoices = []int{0, 1, 2, 5, 10, 15, 30, 60}

func awayLabel(m int) string {
	switch m {
	case 0:
		return "Never"
	case 1:
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", m)
}

func awayLabels() []string {
	out := make([]string, len(awayChoices))
	for i, m := range awayChoices {
		out[i] = awayLabel(m)
	}
	return out
}

func awayIndex() int { return slices.Index(awayChoices, config.Get().Presence.PresenceScreenOff()) }

// withPresence adds the room's rows to the Display card, on a device with a camera: whether the camera
// watches for somebody near, and how long an empty room leaves the screen on.
func withPresence(rows []settingRow) []settingRow {
	if !camera.Available() {
		return rows
	}
	p := config.Get().Presence
	rows = append(rows,
		settingRow{label: "Presence", kind: ctlHeading},
		settingRow{id: "presence", label: "Presence detection", sub: "The camera notices somebody near; nothing is kept",
			kind: ctlToggle, on: p.On})
	if p.On {
		rows = append(rows, settingRow{id: "awayoff", label: "Screen off when nobody is near",
			sub: "Lights again when somebody comes near", kind: ctlChoice, value: awayLabel(p.PresenceScreenOff())})
	}
	return rows
}
