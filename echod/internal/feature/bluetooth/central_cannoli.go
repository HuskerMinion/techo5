//go:build dot && cannoli

package bluetooth

import (
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
)

// pairable is what pairing adds to the advertised features: bluetoothd can bond a device, as on the
// Show and the Spot.
const pairable = esphome.BluetoothPairing

// openCentral is bluetoothd's GATT client on the Echo Dot 5th gen, as on the Show and the Spot. The
// proxy scans through its own raw HCI socket, so bluetoothd has not heard the devices Home Assistant
// asks for, and Connect makes them with Adapter1.ConnectDevice: techo5-bt starts bluetoothd with -E
// for it. The radio's Pause (hardware/ble) stops that scan for the connection itself.
var openCentral = func(value func(string, uint16, []byte), lost func(string)) (central, error) {
	return bluez.OpenCentral(value, lost)
}
