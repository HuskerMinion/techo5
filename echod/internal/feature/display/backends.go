//go:build !dot

package display

import (
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

// What a board's own backends add to the screen, each from a file of its own (an init): nothing here
// knows any board, and with nothing registered the screen is as it ever was.
var (
	// presenceRows are rows for the presence part of the Display card (withPresence).
	presenceRows []func(p config.Presence) []settingRow
	// rowTaps handle a tap on a row of their own; true if it was theirs.
	rowTaps []func(d *Display, id string, p part, opt int) bool
	// frameNotes says something of a frame for the camera test page, under its size.
	frameNotes = func(*camera.Frame) []string { return nil }
	// boardCamTestPath is a board's own account of its camera's path for the camera test, nil for the
	// generic one (camTestPath).
	boardCamTestPath = func() []string { return nil }
)

// boardRowTap is a tap on a row of a board's own.
func (d *Display) boardRowTap(id string, p part, opt int) bool {
	for _, tap := range rowTaps {
		if tap(d, id, p, opt) {
			return true
		}
	}
	return false
}
