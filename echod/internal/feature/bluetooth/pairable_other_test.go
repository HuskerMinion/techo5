//go:build dot || panel

package bluetooth

import "testing"

// The Dot makes no connections and the Panel app has no bluetoothd: neither advertises pairing.
func TestPairingNotAdvertised(t *testing.T) {
	if pairable != 0 {
		t.Error("pairing is advertised where nothing can bond")
	}
}
