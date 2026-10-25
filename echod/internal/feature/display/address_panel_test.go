//go:build panel && !dot

package display

import (
	"net"
	"testing"
)

// A panel shows the address the app reports, the Wi-Fi's before a VPN's, and the network stack's
// when the app has none.
func TestAPanelShowsTheAppsAddress(t *testing.T) {
	wasWiFi, wasAddrs := appWiFi, appAddresses
	t.Cleanup(func() { appWiFi, appAddresses = wasWiFi, wasAddrs })
	appWiFi = func() []net.IP { return []net.IP{net.ParseIP("2001:db8::10"), net.ParseIP("192.0.2.10")} }
	appAddresses = func() []net.IP { return []net.IP{net.ParseIP("100.64.0.10"), net.ParseIP("192.0.2.10")} }
	if got := address(); got != "192.0.2.10" {
		t.Errorf("address = %q, want the Wi-Fi's 192.0.2.10", got)
	}
	appWiFi = func() []net.IP { return nil }
	if got := address(); got != "100.64.0.10" {
		t.Errorf("address without Wi-Fi = %q, want 100.64.0.10", got)
	}
	appAddresses = func() []net.IP { return nil }
	if got := address(); got != stackAddress() {
		t.Errorf("address with none from the app = %q, want the stack's %q", got, stackAddress())
	}
}
