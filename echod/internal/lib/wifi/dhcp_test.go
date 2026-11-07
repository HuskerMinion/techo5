package wifi

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A device that booted with no network has no DHCP client: one is started, with the boot's arguments
// and a script of its own when the boot left none; one that is running is left alone.
func TestADHCPClientIsStartedWhenNoneRuns(t *testing.T) {
	dir := t.TempDir()
	wp, ws, wo, wst, wi := dhcpPid, dhcpScripts, dhcpOwn, startDHC, iface
	t.Cleanup(func() { dhcpPid, dhcpScripts, dhcpOwn, startDHC, iface = wp, ws, wo, wst, wi })
	dhcpPid = filepath.Join(dir, "udhcpc.pid")
	dhcpScripts = []string{filepath.Join(dir, "none.sh")}
	dhcpOwn = filepath.Join(dir, "udhcpc.sh")
	iface = func() string { return "wlan0" }
	var ran []string
	startDHC = func(args ...string) error { ran = args; return nil }

	if !startDHCP() {
		t.Fatal("no client was started with none running")
	}
	want := "-i wlan0 -b -R -t 10 -p " + dhcpPid + " -s " + dhcpOwn
	if strings.Join(ran, " ") != want {
		t.Fatalf("started with %q, want %q", strings.Join(ran, " "), want)
	}
	if b, err := os.ReadFile(dhcpOwn); err != nil || !strings.Contains(string(b), "route add default gw") {
		t.Fatalf("its script: %q %v", b, err)
	}
	// One running (this test's own process stands in for it): left alone.
	if err := os.WriteFile(dhcpPid, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ran = nil
	if startDHCP() || ran != nil {
		t.Fatal("a second client was started beside a running one")
	}
}
