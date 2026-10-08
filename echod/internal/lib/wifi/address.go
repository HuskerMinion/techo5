package wifi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// How the Wi-Fi interface gets its IPv4 address: from DHCP, which is how every device starts, or fixed,
// set on the setup page or with the network_address action. A fixed address suits a network whose DHCP
// server hands out only part of the range, with the rest kept for devices given their own address.
//
// techo5-net (tools/linux/rootfs/usr/local/sbin) puts it on the interface: as it is at boot, when Wi-Fi
// comes back and when a network is joined, as any device does with a fixed address; and on trial when it
// was just changed (ApplyAddress). On trial, a fixed address that another device answers for, or whose
// gateway does not answer from it, is given up for the setting before it, which SaveAddress keeps, and
// why goes to the fallback file: the way a slot that does not come up healthy falls back to the one
// before. A typo in an address must not leave a device out of reach.

// addressFile holds the setting, in a form techo5-net reads as shell variables. Only what was checked
// as an IPv4 address is ever written to it, never text as typed.
var addressFile = "/data/techo5-linux/network.conf"

// fallbackFile says why the last setting tried was not kept.
var fallbackFile = "/run/techo5/network-fallback"

// netTool applies the setting.
var netTool = "/usr/local/sbin/techo5-net"

// Address is the setting.
type Address struct {
	Fixed   bool
	IP      net.IP
	Prefix  int
	Gateway net.IP
	// DNS is the name servers, the gateway when none is given.
	DNS []net.IP
}

// String is the setting in a line: "192.168.1.50/24 via 192.168.1.1", or "automatic (DHCP)".
func (a Address) String() string {
	if !a.Fixed {
		return "automatic (DHCP)"
	}
	return fmt.Sprintf("%s/%d via %s", a.IP, a.Prefix, a.Gateway)
}

// CIDR is the address and its prefix, "192.168.1.50/24"; empty when not fixed.
func (a Address) CIDR() string {
	if !a.Fixed {
		return ""
	}
	return fmt.Sprintf("%s/%d", a.IP, a.Prefix)
}

// DNSList is the name servers, comma separated.
func (a Address) DNSList() string {
	s := make([]string, len(a.DNS))
	for i, d := range a.DNS {
		s[i] = d.String()
	}
	return strings.Join(s, ", ")
}

// ParseAddress reads a setting as people type it: an address with or without its prefix
// ("192.168.1.50/24", or "192.168.1.50" for /24), the gateway, and name servers separated by commas or
// spaces (the gateway when empty). An empty address is DHCP. It refuses what could not work: a gateway
// outside the address's network, the network's own or its broadcast address, anything not IPv4.
func ParseAddress(address, gateway, dns string) (Address, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return Address{}, nil
	}
	a := Address{Fixed: true, Prefix: 24}
	ip := address
	if i := strings.IndexByte(address, '/'); i >= 0 {
		ip = address[:i]
		p, err := strconv.Atoi(strings.TrimSpace(address[i+1:]))
		if err != nil || p < 8 || p > 30 {
			return Address{}, fmt.Errorf("the prefix in %q should be a number from 8 to 30, like /24", address)
		}
		a.Prefix = p
	}
	if a.IP = ipv4(ip); a.IP == nil {
		return Address{}, fmt.Errorf("%q is not an IPv4 address like 192.168.1.50", ip)
	}
	if a.Gateway = ipv4(gateway); a.Gateway == nil {
		return Address{}, fmt.Errorf("the gateway %q is not an IPv4 address like 192.168.1.1", strings.TrimSpace(gateway))
	}
	mask := net.CIDRMask(a.Prefix, 32)
	network := a.IP.Mask(mask)
	broadcast := make(net.IP, 4)
	for i := range broadcast {
		broadcast[i] = network[i] | ^mask[i]
	}
	switch {
	case a.IP.Equal(network) || a.IP.Equal(broadcast):
		return Address{}, fmt.Errorf("%s is the network's own address or its broadcast address in /%d; pick one between them", a.IP, a.Prefix)
	case !a.Gateway.Mask(mask).Equal(network):
		return Address{}, fmt.Errorf("the gateway %s is not in %s/%d, the address's network", a.Gateway, network, a.Prefix)
	case a.Gateway.Equal(a.IP):
		return Address{}, errors.New("the address and the gateway are the same")
	}
	for _, f := range strings.FieldsFunc(dns, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		d := ipv4(f)
		if d == nil {
			return Address{}, fmt.Errorf("the DNS server %q is not an IPv4 address", f)
		}
		a.DNS = append(a.DNS, d)
	}
	if len(a.DNS) == 0 {
		a.DNS = []net.IP{a.Gateway}
	}
	if len(a.DNS) > 3 {
		return Address{}, errors.New("at most three DNS servers")
	}
	return a, nil
}

func ipv4(s string) net.IP {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return nil
	}
	return ip.To4()
}

// LoadAddress is the setting, DHCP when none was made or the file cannot be read.
func LoadAddress() Address {
	b, err := os.ReadFile(addressFile)
	if err != nil {
		return Address{}
	}
	v := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && !strings.HasPrefix(k, "#") {
			v[k] = strings.Trim(val, `"`)
		}
	}
	if v["MODE"] != "static" {
		return Address{}
	}
	a, err := ParseAddress(v["ADDRESS"]+"/"+v["PREFIX"], v["GATEWAY"], v["DNS"])
	if err != nil {
		return Address{}
	}
	return a
}

// SaveAddress keeps the setting, and the one it replaces as the one to go back to if the new one does
// not work; it takes effect with ApplyAddress.
func SaveAddress(a Address) error {
	var b strings.Builder
	b.WriteString("# How wlan0 gets its address. Written by the TECHO5 daemon (the setup page or the\n")
	b.WriteString("# network_address action), read by techo5-net. Change it there, not here.\n")
	if a.Fixed {
		dns := make([]string, len(a.DNS))
		for i, d := range a.DNS {
			dns[i] = d.String()
		}
		fmt.Fprintf(&b, "MODE=static\nADDRESS=%s\nPREFIX=%d\nGATEWAY=%s\nDNS=\"%s\"\n", a.IP, a.Prefix, a.Gateway, strings.Join(dns, " "))
	} else {
		b.WriteString("MODE=dhcp\n")
	}
	if err := os.MkdirAll(filepath.Dir(addressFile), 0o755); err != nil {
		return err
	}
	if old, err := os.ReadFile(addressFile); err == nil {
		if err := os.WriteFile(addressFile+".prev", old, 0o644); err != nil {
			return err
		}
	} else if err := os.WriteFile(addressFile+".prev", []byte("MODE=dhcp\n"), 0o644); err != nil {
		return err
	}
	tmp := addressFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, addressFile)
}

// AddressFallback is why the last setting tried was not kept, empty when it was.
func AddressFallback() string {
	b, err := os.ReadFile(fallbackFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ApplyAddress puts a setting just saved on the interface, on trial: a fixed address that does not work
// is replaced by the setting before it. It takes as long as techo5-net's checks, up to twenty seconds.
// moved is whether the device is now at another address than before.
func ApplyAddress(ctx context.Context) (moved bool, err error) {
	before := address()
	if _, err := os.Stat(netTool); err != nil {
		renewLease()
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, netTool, "try").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("wifi: techo5-net: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// A DHCP lease takes a moment to arrive after the client is started.
	for i := 0; i < 15 && address() == ""; i++ {
		time.Sleep(time.Second)
	}
	now := address()
	return now != "" && now != before, nil
}
