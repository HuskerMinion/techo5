package metrics

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The link report from iw on a Show 8 2nd gen, with the access point's address and name taken out.
const athenaLink = `Connected to <bssid> (on wlan0)
	SSID: <ssid>
	freq: 5180
	RX: 978791061 bytes (1893604 packets)
	TX: 166584691 bytes (603732 packets)
	signal: -35 dBm
	rx bitrate: 433.3 MBit/s VHT-MCS 9 80MHz short GI VHT-NSS 1
	tx bitrate: 390.0 MBit/s VHT-MCS 8 80MHz short GI VHT-NSS 1

	bss flags:	short-slot-time
	dtim period:	1
	beacon int:	100
`

const procDev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:   57601     175    0    0    0     0          0         0    57601     175    0    0    0     0       0          0
 wlan0: 978791061 1893604    0    0    0     0          0         0 166584691  603732    0    8    0     0       0          0
`

// wifiRoot writes the proc files a board has, and leaves out any that are given as "".
func wifiRoot(t *testing.T, wireless string) Reader {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{"proc/net/dev": procDev}
	if wireless != "" {
		files["proc/net/wireless"] = wireless
	}
	for path, body := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Reader{Root: root}
}

// fakeIw stands in for iw for one test, printing what is given, and counts how often it was asked.
func fakeIw(t *testing.T, out string, err error) *int {
	t.Helper()
	calls := 0
	was := iwLink
	iwLink = func(iface string) ([]byte, error) {
		calls++
		if iface != "wlan0" {
			t.Errorf("iw asked about %q, want wlan0", iface)
		}
		return []byte(out), err
	}
	t.Cleanup(func() { iwLink = was })
	return &calls
}

// A board with /proc/net/wireless reads the signal there and never runs iw.
func TestWifiSignalFromProcWireless(t *testing.T) {
	calls := fakeIw(t, athenaLink, nil)
	r := wifiRoot(t, "Inter-| sta-|   Quality        |   Discarded packets               | Missed | WE\n"+
		" face | tus | link level noise |  nwid  crypt   frag  retry   misc | beacon | 22\n"+
		" wlan0: 0000   58.  -52.  -256        0      0      0      0      0        0\n")

	signal, rx, tx := r.Wifi()
	if !signal.Known || signal.Value != -52 {
		t.Errorf("signal = %+v, want -52", signal)
	}
	if *calls != 0 {
		t.Errorf("iw ran %d times on a board that has /proc/net/wireless", *calls)
	}
	if rx.Value != 978791061 || tx.Value != 166584691 {
		t.Errorf("rx, tx = %v, %v", rx.Value, tx.Value)
	}
}

// The Show 8 2nd gen's kernel has no /proc/net/wireless, so the signal is what iw reports for the
// link (#642). The bytes still come from /proc/net/dev.
func TestWifiSignalFromIwWhenProcWirelessIsMissing(t *testing.T) {
	calls := fakeIw(t, athenaLink, nil)
	signal, rx, tx := wifiRoot(t, "").Wifi()

	if !signal.Known || signal.Value != -35 {
		t.Errorf("signal = %+v, want -35", signal)
	}
	if *calls != 1 {
		t.Errorf("iw ran %d times, want once", *calls)
	}
	if rx.Value != 978791061 || tx.Value != 166584691 {
		t.Errorf("rx, tx = %v, %v", rx.Value, tx.Value)
	}
}

// A radio that is not joined, or an image without iw, leaves the signal unknown rather than zero.
func TestWifiSignalUnknownWithoutALink(t *testing.T) {
	for name, iw := range map[string]struct {
		out string
		err error
	}{
		"not connected": {out: "Not connected.\n"},
		"no iw":         {err: errors.New("exec: \"iw\": executable file not found in $PATH")},
	} {
		t.Run(name, func(t *testing.T) {
			fakeIw(t, iw.out, iw.err)
			if signal, _, _ := wifiRoot(t, "").Wifi(); signal.Known {
				t.Errorf("signal = %v, want unknown", signal.Value)
			}
		})
	}
}
