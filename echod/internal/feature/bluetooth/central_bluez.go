//go:build !dot && !panel

package bluetooth

import (
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
)

// pairable is what pairing adds to the advertised features: bluetoothd can bond a device.
const pairable = esphome.BluetoothPairing

// openCentral is bluetoothd's GATT client, which the Show and the Spot share with Bluetooth audio.
var openCentral = func(value func(string, uint16, []byte), lost func(string)) (central, error) {
	return bluez.OpenCentral(value, lost)
}
