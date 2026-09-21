//go:build !dot

package display

import (
	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
)

// The Privacy card's Setup page row. The page is for the settings that are miserable to type here —
// a stream URL, a phone account — and it is off until somebody asks for it, closing itself when it
// is left alone. Getting into it still takes a press on the device, whoever turned it on.

func setupRow() settingRow {
	s := setup.Get()
	row := settingRow{id: "setuppage", label: "Setup page", sub: "Off", kind: ctlToggle}
	switch {
	case s.Waiting():
		row.sub, row.on = "A browser is asking: press the action button", true
	case s.On():
		row.sub, row.on = "Open at this device's address, port 8181", true
	}
	return row
}
