//go:build (!dot || cannoli) && !panel

package bluetooth

import "testing"

// The Show, the Spot and the Echo Dot 5th gen have bluetoothd, which bonds devices, so they advertise pairing.
func TestPairingAdvertised(t *testing.T) {
	if pairable == 0 {
		t.Error("pairing is not advertised where bluetoothd bonds devices")
	}
}
