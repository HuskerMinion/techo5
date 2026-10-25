//go:build panel

package diag

import (
	"context"
	"fmt"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// networkSection is the bundle's network section on a panel. Android runs the tablet's Wi-Fi, and an
// app has no wpa_supplicant to ask, so networkSummary would say "connected=false" of a tablet that is
// online. What the daemon does know is the addresses the app reported (metrics) and whether it has
// Home Assistant's address and token; the addresses are replaced like everything else, so they are
// counted rather than listed.
func networkSection(context.Context) string {
	all, wlan := metrics.Addresses(), metrics.WiFiAddresses()
	ipv4 := 0
	for _, ip := range all {
		if ip.To4() != nil {
			ipv4++
		}
	}
	return strings.Join([]string{
		"wifi: managed by Android",
		fmt.Sprintf("addresses reported by the app: %d (%d IPv4, %d on Wi-Fi)", len(all), ipv4, len(wlan)),
		fmt.Sprintf("home assistant access: set=%t", hass.Get().Ready()),
	}, "\n")
}
