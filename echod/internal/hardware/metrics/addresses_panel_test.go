//go:build panel

package metrics

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fakeNet points netPath at a file holding lines in the app's files/net format.
func fakeNet(t *testing.T, lines string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "net")
	if err := os.WriteFile(p, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := netPath
	netPath = p
	t.Cleanup(func() { netPath = saved })
}

// USB tethering reaches only the computer on the cable: neither its address nor its interface is
// handed to mDNS, while Wi-Fi's are.
func TestTetheringIsNeverAdvertised(t *testing.T) {
	up := strconv.FormatUint(uint64(net.FlagUp|net.FlagBroadcast|net.FlagMulticast), 10)
	fakeNet(t, ""+
		"wlan0 3 1500 "+up+" 192.0.2.10/24\n"+
		"rndis0 7 1500 "+up+" 192.0.2.129/25\n"+
		"usb0 8 1500 "+up+" 198.51.100.2/24\n")

	got := Addresses()
	if len(got) != 1 || got[0].String() != "192.0.2.10" {
		t.Errorf("Addresses = %v, want only Wi-Fi's 192.0.2.10", got)
	}
	ifs := MulticastInterfaces()
	if len(ifs) != 1 || ifs[0].Name != "wlan0" {
		t.Errorf("MulticastInterfaces = %v, want only wlan0", ifs)
	}
	// Not advertised, but the device's own all the same.
	own := OwnAddresses()
	for _, want := range []string{"192.0.2.10", "192.0.2.129", "198.51.100.2"} {
		found := false
		for _, ip := range own {
			found = found || ip.String() == want
		}
		if !found {
			t.Errorf("OwnAddresses = %v, missing %s", own, want)
		}
	}
}
