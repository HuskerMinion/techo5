//go:build panel

package bluetooth

import "github.com/HuskerMinion/techo5/echod/internal/lib/panelctl"

// Keep the shared connection manager, with the capacity supported by the app bridge.
const slots = 1
const proxyBeaconSupported = false

func proxyPermission(on bool) {
	if on {
		panelctl.Send("ble 1")
	} else {
		panelctl.Send("ble 0")
	}
}
