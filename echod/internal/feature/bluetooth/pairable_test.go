//go:build !dot && !panel

package bluetooth

import "testing"

// The Show and the Spot have bluetoothd, which bonds devices, so they advertise pairing.
func TestPairingAdvertised(t *testing.T) {
	if pairable == 0 {
		t.Error("pairing is not advertised where bluetoothd bonds devices")
	}
}
