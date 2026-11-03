package layout

import (
	"os"
	"path/filepath"
	"testing"
)

// wlan0 wherever there is one; otherwise the interface the kernel marks wireless (the Surface RT's
// mlan0), and wlan0 again where there is none.
func TestTheWifiInterfaceIsFound(t *testing.T) {
	mk := func(names ...string) string {
		dir := t.TempDir()
		for _, n := range names {
			if err := os.MkdirAll(filepath.Join(dir, n), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	for _, c := range []struct {
		name string
		dir  string
		want string
	}{
		{"an Echo", mk("lo", "wlan0", "wlan0/wireless"), "wlan0"},
		{"the Surface RT", mk("lo", "mlan0", "mlan0/wireless"), "mlan0"},
		{"nothing wireless", mk("lo", "eth0"), "wlan0"},
	} {
		if got := wifiInterface(c.dir); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
