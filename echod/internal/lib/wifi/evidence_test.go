package wifi

import (
	"strings"
	"testing"
)

// What goes into the log is read by strangers once it is in a diagnostics file: the network's name,
// the access point's MAC and any address must not be in it.
func TestEvidenceKeepsTheLinkAndNothingThatNamesIt(t *testing.T) {
	status := `bssid=aa:bb:cc:dd:ee:ff
freq=2412
ssid=Our Home Network
id=0
mode=station
pairwise_cipher=CCMP
group_cipher=CCMP
key_mgmt=WPA2-PSK
wpa_state=COMPLETED
ip_address=192.0.2.44
address=11:22:33:44:55:66`
	got := statusEvidence(status)
	for _, want := range []string{"wpa_state=COMPLETED", "freq=2412", "group_cipher=CCMP", "key_mgmt=WPA2-PSK"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
	for _, leak := range []string{"Our Home", "aa:bb", "11:22", "192.0.2"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaked into %q", leak, got)
		}
	}

	kernel := `[   18.2] something else entirely
[   19.1] [wlan]kalIndicateStatusAndComplete:(AIS INFO) wlan0 netif_carrier_on [ssid:Our Home Network]
[   19.2] [355:wpa_supplicant][wlan]wlanProcessSecurityFrame:(RSN INFO)T1X len=135
[   19.3] [355:wpa_supplicant][wlan]wlanProcessSecurityFrame:(RSN INFO)T1X len=113
[  782.2] [355:wpa_supplicant][wlan]wlanProcessSecurityFrame:(RSN INFO)T1X len=113`
	lines := kernelEvidence(kernel, 3)
	if len(lines) != 3 || !strings.HasSuffix(lines[2], "T1X len=113") || !strings.Contains(lines[2], "782.2") {
		t.Errorf("kept %q, want the last three link lines ending in the rekey", lines)
	}
	all := strings.Join(kernelEvidence(kernel, 10), "\n")
	if strings.Contains(all, "Our Home") || !strings.Contains(all, "ssid:<ssid>") {
		t.Errorf("the network's name was not taken out: %q", all)
	}
	if got := redactSSID("RSN: ssid=Guest Wi-Fi 5G, group rekey"); strings.Contains(got, "Guest") || strings.Contains(got, "5G") {
		t.Errorf("a loose name leaked: %q", got)
	}
	if strings.Contains(all, "something else") {
		t.Errorf("a line that is not about the link was kept: %q", all)
	}
}
