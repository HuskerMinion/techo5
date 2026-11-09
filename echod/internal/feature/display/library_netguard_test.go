//go:build !dot && !spot

package display

import (
	"net"

	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
)

// The library's picture tests serve covers from a test server on this machine, which the network
// guard refuses on a device. Here only loopback is let through, the rest as on a device.
func init() {
	was := netguard.Refused
	netguard.Refused = func(ip net.IP) bool { return !ip.IsLoopback() && was(ip) }
}
