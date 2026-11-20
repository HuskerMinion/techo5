//go:build !dot && !panel

package ble

import (
	"bytes"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
)

// A device that names its service with a full UUID, as a Flic button does in pairing mode, has to
// reach Home Assistant with it, little-endian in a 128-bit list beside the 16-bit one.
func TestFullServiceUUIDsReachHomeAssistant(t *testing.T) {
	a, ok := fromDevice(bluez.Device{Address: "12:34:56:78:9A:BC", UUIDs: []string{
		"0000180f-0000-1000-8000-00805f9b34fb",
		"00C90000-2CBD-4F2A-A725-5CCD960FFB7D",
	}})
	if !ok {
		t.Fatal("fromDevice refused a device with services")
	}
	want := []byte{
		3, 0x03, 0x0f, 0x18,
		17, 0x07, 0x7d, 0xfb, 0x0f, 0x96, 0xcd, 0x5c, 0x25, 0xa7, 0x2a, 0x4f, 0xbd, 0x2c, 0x00, 0x00, 0xc9, 0x00,
	}
	if !bytes.Equal(a.Data, want) {
		t.Errorf("Data = % x\nwant   % x", a.Data, want)
	}
}

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
