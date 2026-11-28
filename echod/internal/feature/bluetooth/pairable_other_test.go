//go:build (dot && !cannoli) || panel

package bluetooth

import "testing"

// The Dot 2nd and 3rd gen make no connections and the Panel app has no bluetoothd: neither advertises pairing.
func TestPairingNotAdvertised(t *testing.T) {
	if pairable != 0 {
		t.Error("pairing is advertised where nothing can bond")
	}
}
