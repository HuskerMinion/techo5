package netguard

import (
	"net"
	"strings"
	"testing"
)

// The device's own LAN address is refused though it is not loopback; another LAN address is not.
func TestOwnAddressRefused(t *testing.T) {
	saved := OwnIP
	t.Cleanup(func() { OwnIP = saved })
	OwnIP = func(ip net.IP) bool { return ip.Equal(net.ParseIP("192.168.1.50")) }
	for ip, want := range map[string]bool{
		"192.168.1.50": true, "192.168.1.20": false, "127.0.0.1": true, "169.254.1.1": true,
		"224.0.0.1": true, "::1": true, "fe80::1": true, "0.0.0.0": true, "8.8.8.8": false,
	} {
		if got := Refused(net.ParseIP(ip)); got != want {
			t.Errorf("Refused(%s) = %v, want %v", ip, got, want)
		}
	}
	if !Refused(nil) {
		t.Error("an address that did not parse was allowed")
	}
}

// The dialer's Control sees the address actually dialed.
func TestDialerControl(t *testing.T) {
	saved := OwnIP
	t.Cleanup(func() { OwnIP = saved })
	OwnIP = func(ip net.IP) bool { return ip.Equal(net.ParseIP("192.168.1.50")) }
	ctl := Dialer(0).Control
	if err := ctl("tcp", "192.168.1.50:80", nil); err == nil || !strings.Contains(err.Error(), "192.168.1.50") {
		t.Errorf("own address: %v", err)
	}
	if err := ctl("tcp", "192.168.1.20:80", nil); err != nil {
		t.Errorf("other LAN address: %v", err)
	}
	if err := ctl("tcp", "nonsense", nil); err == nil {
		t.Error("an address without a port was allowed")
	}
}
