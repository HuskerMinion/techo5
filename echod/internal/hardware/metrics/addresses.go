package metrics

import (
	"net"
	"slices"
	"strings"
)

// AddressKey is a set of addresses as one comparable string, so a change is one comparison.
func AddressKey(ips []net.IP) string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}
