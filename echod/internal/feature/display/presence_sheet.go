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
		return tr("ui_never")
	case 1:
		return tr("one_minute")
	}
	return fmt.Sprintf(tr("ui_d_minutes"), m)
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
		settingRow{label: tr("presence_heading"), kind: ctlHeading},
		settingRow{id: "presence", label: tr("presence_label"), sub: tr("presence_description"),
			kind: ctlToggle, on: p.On})
	if p.On {
		rows = append(rows, settingRow{id: "awayoff", label: tr("screen_off_when_nobody_is_near"),
			sub: tr("presence_return"), kind: ctlChoice, value: awayLabel(p.PresenceScreenOff())})
	}
	return rows
}
