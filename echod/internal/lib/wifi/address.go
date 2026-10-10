package wifi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// How the Wi-Fi interface gets its IPv4 address: from DHCP, which is how every device starts, or fixed,
// set on the setup page, on the screen or with the network_address action. A fixed address suits a
// network whose DHCP server hands out only part of the range, with the rest kept for devices given their
// own address, and keeps a device where it is when Wi-Fi is switched off overnight.
//
// techo5-net (tools/linux/rootfs/usr/local/sbin) puts it on the interface. A change is written as a
// candidate beside the setting in use, and techo5-net tries it (ChangeAddress): a fixed address that
// another device answers for, or whose gateway does not answer from it, is dropped, the setting in use
// stays, and why goes to the fallback file. Only a candidate that worked becomes the setting, so a
// restart in the middle of a trial comes back on the last one that worked. A typo in an address must
// not leave a device out of reach.
//
// At boot and when Wi-Fi comes back, the setting is put on as it is, as on any device with a fixed
// address; on another network than it was set on, or with its gateway silent for five minutes, the
// device uses DHCP for the rest of the session instead (techo5-net up and check).

// addressFile holds the setting in use, in KEY=value lines techo5-net reads and checks. Only what was
// checked as an IPv4 address is ever written to it, never text as typed.
var addressFile = "/data/techo5-linux/network.conf"

// candidate is where a changed setting waits for its trial.
func candidate() string { return addressFile + ".new" }

// fallbackFile says why the last setting tried was not kept.
var fallbackFile = "/run/techo5/network-fallback"

// netTool applies the setting.
var netTool = "/usr/local/sbin/techo5-net"

// changing is held for a whole change, written and tried: two Saves close together (a double click, a
// reload, a script retrying, the setup page and the action at once) are tried one after the other, never
// over each other.
var changing sync.Mutex

// AddressSupported is whether this image can put a fixed address on: an image without techo5-net would
// save one and never try it, so the setting is not offered there.
func AddressSupported() bool {
	_, err := os.Stat(netTool)
	return err == nil
}

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

// same is whether two settings put the same thing on the interface.
func (a Address) same(b Address) bool {
	return a.String() == b.String() && a.DNSList() == b.DNSList()
}

// ParseAddress reads a setting as people type it: an address with or without its prefix
// ("192.168.1.50/24", or "192.168.1.50" for /24), the gateway, and name servers separated by commas or
// spaces (the gateway when empty). An empty address is DHCP. It refuses what could not work: an address
// or gateway no network hands to a device (0.x, loopback, link-local, multicast, reserved), a gateway
// outside the address's network or on its network or broadcast address, the network's own or its
// broadcast address, a name server nobody could answer from, anything not IPv4.
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
	if !hostable(a.IP) {
		return Address{}, fmt.Errorf("%s is not an address a network gives a device", a.IP)
	}
	if a.Gateway = ipv4(gateway); a.Gateway == nil {
		return Address{}, fmt.Errorf("the gateway %q is not an IPv4 address like 192.168.1.1", strings.TrimSpace(gateway))
	}
	if !hostable(a.Gateway) {
		return Address{}, fmt.Errorf("the gateway %s is not an address a router has", a.Gateway)
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
	case a.Gateway.Equal(network) || a.Gateway.Equal(broadcast):
		return Address{}, fmt.Errorf("the gateway %s is the network's own address or its broadcast address", a.Gateway)
	case a.Gateway.Equal(a.IP):
		return Address{}, errors.New("the address and the gateway are the same")
	}
	for _, f := range strings.FieldsFunc(dns, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		d := ipv4(f)
		if d == nil {
			return Address{}, fmt.Errorf("the DNS server %q is not an IPv4 address", f)
		}
		if !hostable(d) {
			return Address{}, fmt.Errorf("%s cannot be a DNS server", d)
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

// hostable is whether an IPv4 address can belong to a device on a network: not 0.x ("this network"),
// loopback, link-local, multicast or 240 and up (reserved, and 255.255.255.255).
func hostable(ip net.IP) bool {
	switch {
	case ip[0] == 0, ip.IsLoopback(), ip.IsLinkLocalUnicast(), ip.IsMulticast(), ip[0] >= 240:
		return false
	}
	return true
}

// LoadAddress is the setting in use, DHCP when none was made or the file cannot be read. A file missing
// any of its values reads as DHCP, as techo5-net reads it.
func LoadAddress() Address {
	return readAddress(addressFile)
}

func readAddress(path string) Address {
	b, err := os.ReadFile(path)
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
	if v["MODE"] != "static" || v["ADDRESS"] == "" || v["PREFIX"] == "" || v["GATEWAY"] == "" {
		return Address{}
	}
	a, err := ParseAddress(v["ADDRESS"]+"/"+v["PREFIX"], v["GATEWAY"], v["DNS"])
	if err != nil {
		return Address{}
	}
	return a
}

// writeCandidate writes a changed setting for techo5-net to try. ssid is the network joined now, in hex
// of the form wpa_cli prints it: a fixed address is for the network it was set on.
func writeCandidate(a Address, ssid string) error {
	var b strings.Builder
	b.WriteString("# How wlan0 gets its address. Written by the TECHO5 daemon (the setup page, the screen\n")
	b.WriteString("# or the network_address action), read by techo5-net. Change it there, not here.\n")
	if a.Fixed {
		dns := make([]string, len(a.DNS))
		for i, d := range a.DNS {
			dns[i] = d.String()
		}
		fmt.Fprintf(&b, "MODE=static\nADDRESS=%s\nPREFIX=%d\nGATEWAY=%s\nDNS=\"%s\"\nSSID=%s\n",
			a.IP, a.Prefix, a.Gateway, strings.Join(dns, " "), ssid)
	} else {
		b.WriteString("MODE=dhcp\n")
	}
	if err := os.MkdirAll(filepath.Dir(addressFile), 0o755); err != nil {
		return err
	}
	tmp := candidate() + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, candidate())
}

// AddressFallback is why the last setting tried was not kept, empty when it was.
func AddressFallback() string {
	b, err := os.ReadFile(fallbackFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// AddressUnchanged is whether a setting is the one in use, which a Save need not try again: an unchanged
// DHCP setting would otherwise take the device off the network for a new lease, for nothing.
func AddressUnchanged(a Address) bool {
	return a.same(LoadAddress())
}

// ChangeAddress tries a setting and keeps it if it works: written as the candidate, tried by techo5-net,
// made the setting in use only when it passed. It waits for any change already under way. It takes as
// long as techo5-net's checks and then a lease, up to about a minute. moved is whether the device is
// now at another address than before; an unchanged setting is not tried at all.
func ChangeAddress(ctx context.Context, a Address) (moved bool, err error) {
	changing.Lock()
	defer changing.Unlock()
	if AddressUnchanged(a) {
		return false, nil
	}
	if !AddressSupported() {
		return false, errors.New("wifi: this image cannot set an address (no techo5-net)")
	}
	before := address()
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	ssid := rawSSID(sctx)
	cancel()
	if err := writeCandidate(a, hex.EncodeToString([]byte(ssid))); err != nil {
		return false, err
	}
	tctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(tctx, netTool, "try").CombinedOutput()
	if err != nil {
		_ = os.Remove(candidate())
		return false, fmt.Errorf("wifi: techo5-net: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// A DHCP lease takes a moment to arrive after the client is started.
	for i := 0; i < 15 && address() == ""; i++ {
		time.Sleep(time.Second)
	}
	now := address()
	return now != "" && now != before, nil
}

// rawSSID is the network joined now as wpa_cli prints it, escapes and all, which is what techo5-net
// compares with; empty when not joined.
func rawSSID(ctx context.Context) string {
	out, err := cli(ctx, "status")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "ssid="); ok {
			return v
		}
	}
	return ""
}
