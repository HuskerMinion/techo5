//go:build !dot

package ble

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
)

// BlueZ prints the address; rebuilt for Home Assistant it has to come out the same.
func TestBlueZAddressReachesHomeAssistantAsPrinted(t *testing.T) {
	a, ok := fromDevice(bluez.Device{Address: "12:34:56:78:9A:BC", AddressType: "random", RSSI: -92, Name: "sensor"})
	if !ok {
		t.Fatal("fromDevice refused a device with a name")
	}
	if got := a.Addr(); got != 0x123456789ABC {
		t.Errorf("Addr() = %012X, want 123456789ABC", got)
	}
}
