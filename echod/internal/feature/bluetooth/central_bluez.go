//go:build !dot

package bluetooth

import (
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
)

// openCentral is bluetoothd's GATT client, which the Show and the Spot share with Bluetooth audio.
var openCentral = func(value func(string, uint16, []byte), lost func(string)) (central, error) {
	return bluez.OpenCentral(value, lost)
}
