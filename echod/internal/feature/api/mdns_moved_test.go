package api

import (
	"net"
	"testing"
)

// Only an IPv4 address giving way to another is a move, which drops Home Assistant's connections and
// keeps the device unannounced for two minutes: IPv6 addresses arriving with SLAAC or rotating, and a
// first lease at boot, cost nothing.
func TestOnlyANewIPv4AddressIsAMove(t *testing.T) {
	ip := func(s ...string) []net.IP {
		var out []net.IP
		for _, a := range s {
			out = append(out, net.ParseIP(a))
		}
		return out
	}
	for _, c := range []struct {
		name     string
		was, now []net.IP
		want     bool
	}{
		{"a fixed address replaces the lease", ip("192.0.2.61"), ip("192.0.2.62"), true},
		{"the same address", ip("192.0.2.61"), ip("192.0.2.61"), false},
		{"a first lease", nil, ip("192.0.2.61"), false},
		{"the address lost", ip("192.0.2.61"), nil, false},
		{"SLAAC after the lease", ip("192.0.2.61"), ip("192.0.2.61", "2001:db8::5"), false},
		{"a temporary address rotating", ip("192.0.2.61", "2001:db8::5"), ip("192.0.2.61", "2001:db8::9"), false},
		{"moved with IPv6 beside it", ip("192.0.2.61", "2001:db8::5"), ip("192.0.2.62", "2001:db8::5"), true},
	} {
		if got := movedIPv4(c.was, c.now); got != c.want {
			t.Errorf("%s: moved %v, want %v", c.name, got, c.want)
		}
	}
}
