package firstrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// After a reset, no file a boot reads a network from still holds one: the supplicant's configuration
// has none (and is not empty, so no boot imports into it), the Dot's installer network and Fire OS's
// are gone, and athena's import is marked done.
func TestResetLeavesNoNetworkToComeBack(t *testing.T) {
	fresh(t)
	dir := t.TempDir()
	linux := filepath.Join(dir, "techo5-linux")
	android := filepath.Join(dir, "misc", "wifi")
	for _, d := range []string{linux, android} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	network := "ctrl_interface=/run/wpa\nnetwork={\n\tssid=\"Old Home\"\n\tpsk=\"secret words\"\n}\n"
	conf := filepath.Join(linux, "wpa_supplicant.conf")
	files := map[string]string{
		conf:                                 network,
		conf + ".prev":                       network,
		filepath.Join(linux, "wifi.conf"):    "ssid=\"Old Home\"\npsk=\"secret words\"\n",
		filepath.Join(linux, "network.conf"): "address=192.168.1.20/24\n",
		filepath.Join(android, "wpa_supplicant.conf"): network,
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	wasConf, wasOwn, wasFire, wasAddr, wasRestart := wifiConf, ownWifi, fireOSWifi, addressWifi, restart
	t.Cleanup(func() {
		wifiConf, ownWifi, fireOSWifi, addressWifi, restart = wasConf, wasOwn, wasFire, wasAddr, wasRestart
	})
	wifiConf = func() string { return conf }
	ownWifi, fireOSWifi = filepath.Join(linux, "wifi.conf"), filepath.Join(android, "wpa_supplicant.conf")
	addressWifi = filepath.Join(linux, "network.conf")
	restarted := make(chan struct{}, 1)
	restart = func() { restarted <- struct{}{} }

	if err := Reset(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("it did not restart")
	}
	for p := range files {
		b, err := os.ReadFile(p)
		if err == nil && strings.Contains(string(b), "Old Home") {
			t.Errorf("%s still has the old network", p)
		}
	}
	b, err := os.ReadFile(conf)
	if err != nil || string(b) != emptyWifi {
		t.Errorf("the configuration is %q (%v), want one with no network", b, err)
	}
	if _, err := os.Stat(filepath.Join(linux, ".wifi-imported")); err != nil {
		t.Error("the import is not marked done, so athena's boot would bring Fire OS's networks back")
	}
	// The wipe at the next start does it again, and finds nothing left.
	Settle()
	if b, _ := os.ReadFile(conf); string(b) != emptyWifi {
		t.Errorf("after the start the configuration is %q", b)
	}
}
