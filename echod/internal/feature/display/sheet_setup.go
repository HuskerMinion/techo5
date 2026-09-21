//go:build !dot

package display

import (
	"fmt"

	"github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
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
		row.sub, row.on = "Open "+setupURL(), true
	}
	return row
}

// setupURL is what to type into a browser: the address this device is on, not its name. A name is
// what the device is called in Home Assistant, which is not what a browser resolves, and somebody
// standing at the screen with a phone in their hand needs the thing they can type.
func setupURL() string {
	for _, ip := range metrics.Addresses() {
		if v4 := ip.To4(); v4 != nil {
			return fmt.Sprintf("http://%s:%d", v4, web.Port)
		}
	}
	return fmt.Sprintf("this device's address, port %d", web.Port)
}
