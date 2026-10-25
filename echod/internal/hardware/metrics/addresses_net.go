//go:build !panel

package metrics

import (
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Addresses is every address something else on the network could reach the device at. Loopback and
// link-local are left out: neither is an answer to "where is it". So is a USB gadget link (the bench
// network on the USB cable, athena's RNDIS at 172.16.42.1): it reaches only the computer at the other
// end of the cable, and advertised over mDNS it became the address Home Assistant kept for the device
// and could not connect to (unit 1, 2026-10-15). Nor is the Tailscale interface (feature/tailscale):
// its address is the tailnet's, which the house's devices and Home Assistant at home must not be sent
// to; a device from away reaches it by its tailnet name.
//
// Not a Reader: this comes from the network stack rather than from a file under Root, so there is
// nothing for a test to point somewhere else.
func Addresses() []net.IP {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, i := range ifs {
		if usbGadget(i.Name) || strings.HasPrefix(i.Name, "tailscale") {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		ips = append(ips, reachable(addrs)...)
	}
	return ips
}

// netClass is where the kernel lists network interfaces; a variable for the tests.
var netClass = "/sys/class/net"

// usbGadget is whether an interface is a USB gadget link: one the device itself serves on its USB
// port (the RNDIS and ECM functions; see gadgetDevice), or a bridge with only such links in it (the
// bench shells put them on br0). Told by the device rather than by the name, so a USB Ethernet
// adapter plugged into a device, which the kernel may also call usb0, is a network like any other:
// its device is the adapter's USB interface.
func usbGadget(name string) bool {
	dir := filepath.Join(netClass, name)
	if ports, err := os.ReadDir(filepath.Join(dir, "brif")); err == nil {
		if len(ports) == 0 {
			return false
		}
		for _, p := range ports {
			if !gadgetDevice(filepath.Join(netClass, p.Name())) {
				return false
			}
		}
		return true
	}
	return gadgetDevice(dir)
}

// gadgetDevice is whether the interface at dir in sysfs belongs to a USB gadget: the kernel's gadget
// Ethernet code marks it DEVTYPE=gadget, and its device is the gadget.
func gadgetDevice(dir string) bool {
	if b, err := os.ReadFile(filepath.Join(dir, "uevent")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(line) == "DEVTYPE=gadget" {
				return true
			}
		}
	}
	dev, err := filepath.EvalSymlinks(filepath.Join(dir, "device"))
	if err != nil {
		return false
	}
	base := filepath.Base(dev)
	return base == "gadget" || strings.HasPrefix(base, "gadget.")
}

// WiFiAddresses is the addresses of Addresses on the Wi-Fi interface alone: the network's own, without a
// VPN's beside them.
func WiFiAddresses() []net.IP {
	i, err := net.InterfaceByName(wifiInterface)
	if err != nil {
		return nil
	}
	addrs, err := i.Addrs()
	if err != nil {
		return nil
	}
	return reachable(addrs)
}

// reachable is the addresses something else on the network could reach: loopback and link-local are
// left out, since neither is an answer to "where is it".
func reachable(addrs []net.Addr) []net.IP {
	var ips []net.IP
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if ok && ipnet.IP.IsGlobalUnicast() && !ipnet.IP.IsLinkLocalUnicast() {
			ips = append(ips, ipnet.IP)
		}
	}
	return ips
}

// MulticastInterfaces is nil here: the mDNS library lists the interfaces itself.
func MulticastInterfaces() []net.Interface { return nil }
