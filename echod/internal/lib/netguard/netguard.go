// Package netguard keeps a connection the daemon makes for somebody else's address off the device
// itself: loopback, its own addresses, link-local and multicast. The rule is applied to the address
// actually dialed, so it holds whatever the name was, whatever DNS answered and after any redirect.
package netguard

import (
	"fmt"
	"net"
	"syscall"
	"time"
)

// Refused is whether a connection to ip is refused; a variable for the tests. An address that does not
// parse is refused too.
var Refused = func(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || OwnIP(ip)
}

// OwnIP is whether ip is one of the device's own addresses, read at each connection; not knowing them,
// every address is taken for its own.
var OwnIP = func(ip net.IP) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return true
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// Dialer makes connections that are refused, as they are made, when Refused says so for the address
// dialed. Control runs after the name is resolved, once per address tried.
func Dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if Refused(net.ParseIP(host)) {
				return fmt.Errorf("the device may not reach %s", host)
			}
			return nil
		},
	}
}
