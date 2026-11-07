//go:build panel

package firstrun

import (
	"net"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// On a TECHO5 Panel the tablet's own Wi-Fi settings join the network and the app installs its own
// updates, so those two steps are left out, and the address Home Assistant is pointed at is the one the
// app reports (Android refuses the app the interface list).
func init() {
	steps = []string{Hello, Zone, Name, Home, Sound, Done}
	WifiStatus = func() wifi.Status {
		for _, ips := range [][]net.IP{metrics.WiFiAddresses(), metrics.Addresses()} {
			for _, ip := range ips {
				if ip4 := ip.To4(); ip4 != nil {
					return wifi.Status{Connected: true, Address: ip4.String(), State: "completed"}
				}
			}
		}
		return wifi.Status{}
	}
}
