//go:build panel && !dot

package display

import (
	"net"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// On a panel the address shown is one the app reports: Android 11 and later refuse an app the netlink
// request net.Interfaces makes, and the settings sheet showed "-". The Wi-Fi's own first, then any
// other's. Without an IPv4 address from the app, it is the network stack's answer as elsewhere.
func init() {
	address = panelAddress
}

// appWiFi and appAddresses are what the app reported (metrics). Variables, for tests.
var (
	appWiFi      = metrics.WiFiAddresses
	appAddresses = metrics.Addresses
)

func panelAddress() string {
	for _, ips := range [][]net.IP{appWiFi(), appAddresses()} {
		for _, ip := range ips {
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return stackAddress()
}
