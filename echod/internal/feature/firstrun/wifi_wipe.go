package firstrun

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// Where a network can come back from after a reset, beside the supplicant's own configuration
// (wifi.Conf, /data/techo5-linux/wpa_supplicant.conf): every board's boot imports one when that holds
// none. The gift installers clear the same (install-show.py, install-athena.py, install-dot.py).
var (
	// ownWifi is the network the Dot's installer set (wifi-set), which techo5-net builds the
	// configuration from when it has none newer of its own.
	ownWifi = "/data/techo5-linux/wifi.conf"
	// fireOSWifi is Fire OS's saved network on a Dot, techo5-net's last resort.
	fireOSWifi = "/data/misc/wifi/wpa_supplicant.conf"
	// addressWifi is a fixed address set for the old home's network (lib/wifi/address.go).
	addressWifi = "/data/techo5-linux/network.conf"
	// wifiConf is the supplicant's configuration (a variable for the tests).
	wifiConf = func() string { return wifi.Conf }
)

// emptyWifi is a supplicant configuration with no network: the supplicant still starts, so a screen's
// Wi-Fi page and Improv have something to talk to, and no boot imports into a file that is not empty
// (t5_wifi_conf on a Show, a Spot and the Skylight; techo5-net on a Dot, which keeps it over the
// installer's once that is gone).
const emptyWifi = "ctrl_interface=/run/wpa\nupdate_config=0\n"

// wipeWifi leaves the device with no network to join and none to import at the next boot. On athena,
// whose boot imports Fire OS's networks into a configuration with none until one import has worked,
// .wifi-imported says one has. A device whose supplicant is not ours (a Panel's tablet) has none of
// these and is left alone.
func wipeWifi() {
	conf := wifiConf()
	dir := filepath.Dir(conf)
	if _, err := os.Stat(dir); err != nil {
		return
	}
	for _, p := range []string{ownWifi, fireOSWifi, addressWifi, conf + ".prev", conf + ".tmp"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("reset: removing a saved network failed", "file", p, "err", err)
		}
	}
	tmp := conf + ".new"
	if err := os.WriteFile(tmp, []byte(emptyWifi), 0o600); err == nil {
		err = os.Rename(tmp, conf)
		if err != nil {
			slog.Warn("reset: emptying the Wi-Fi configuration failed", "err", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".wifi-imported"), nil, 0o600); err != nil {
		slog.Warn("reset: marking Wi-Fi imported failed", "err", err)
	}
}
