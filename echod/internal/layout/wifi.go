package layout

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// netDir is where the kernel lists the network interfaces. A variable, for tests.
var netDir = "/sys/class/net"

// WifiInterface is the Wi-Fi interface's name. Every Echo's driver calls it wlan0; the Surface RT's
// mwifiex calls it mlan0. Where there is no wlan0 the first interface the kernel marks as wireless is
// it, and where there is none of those either, wlan0, as before. Looked for once, when first asked:
// the driver is up long before the daemon starts.
var WifiInterface = sync.OnceValue(func() string { return wifiInterface(netDir) })

func wifiInterface(dir string) string {
	const echo = "wlan0"
	if _, err := os.Stat(filepath.Join(dir, echo)); err == nil {
		return echo
	}
	wireless, _ := filepath.Glob(filepath.Join(dir, "*", "wireless"))
	sort.Strings(wireless)
	if len(wireless) > 0 {
		return filepath.Base(filepath.Dir(wireless[0]))
	}
	return echo
}
