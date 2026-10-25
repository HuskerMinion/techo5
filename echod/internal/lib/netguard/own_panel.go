//go:build panel

package netguard

import (
	"net"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// On a panel the device's own addresses are the ones the app reports: Android 11 and later refuse an app
// the netlink request net.InterfaceAddrs makes (metrics/addresses_panel.go). Every interface it
// reports counts, USB tethering's too, which is left out only of what mDNS advertises. Before the app
// has said, it is the network stack's answer as elsewhere, which on Android is an error and so every
// address.
func init() {
	stack := OwnIP
	OwnIP = func(ip net.IP) bool {
		own := metrics.OwnAddresses()
		if len(own) == 0 {
			return stack(ip)
		}
		for _, a := range own {
			if a.Equal(ip) {
				return true
			}
		}
		return false
	}
}
