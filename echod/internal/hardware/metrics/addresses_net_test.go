//go:build !panel

package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

// A sysfs of network interfaces: each name's device links to a directory with the given base name
// ("" for none, as for loopback or a VPN), and bridges list their ports under brif.
func fakeNetClass(t *testing.T, devices map[string]string, bridges map[string][]string) {
	t.Helper()
	root := t.TempDir()
	class := filepath.Join(root, "class", "net")
	for name, dev := range devices {
		dir := filepath.Join(class, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if dev == "" {
			continue
		}
		target := filepath.Join(root, "devices", name, dev)
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "device")); err != nil {
			t.Skipf("no symlinks here: %v", err)
		}
	}
	for name, ports := range bridges {
		brif := filepath.Join(class, name, "brif")
		if err := os.MkdirAll(brif, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, p := range ports {
			if err := os.WriteFile(filepath.Join(brif, p), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A gadget link told by its uevent alone.
	dir := filepath.Join(class, "usb3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "uevent"), []byte("DEVTYPE=gadget\nINTERFACE=usb3\nIFINDEX=7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := netClass
	netClass = class
	t.Cleanup(func() { netClass = saved })
}

func TestUSBGadgetLeftOut(t *testing.T) {
	fakeNetClass(t, map[string]string{
		"rndis0": "gadget",   // Amazon's kernels
		"usb0":   "gadget.0", // mainline
		"usb1":   "gadget.0",
		"wlan0":  "mmc1:0001:1",
		"eth0":   "1-1:1.0",
		"usb2":   "1-1:1.0", // a USB Ethernet adapter the kernel named usb2
		"wg0":    "",
		"lo":     "",
	}, map[string][]string{
		"br0": {"rndis0", "usb0"},
		"br1": {"usb0", "eth0"},
		"br2": {},
	})
	for name, want := range map[string]bool{
		"rndis0": true, "usb0": true, "usb1": true, "usb3": true, "br0": true,
		"wlan0": false, "eth0": false, "usb2": false, "wg0": false, "lo": false, "br1": false, "br2": false,
		"missing0": false,
	} {
		if got := usbGadget(name); got != want {
			t.Errorf("usbGadget(%q) = %v, want %v", name, got, want)
		}
	}
}
