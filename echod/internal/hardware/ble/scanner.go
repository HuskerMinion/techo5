package ble

import (
	"errors"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

// ErrScanUnavailable means scanning must wait for permission or an enabled adapter.
// Callers should retry without reporting a broken scanner to Home Assistant.
var ErrScanUnavailable = errors.New("ble: scanning is not available yet")

// ScanLost fires when a scan ends that nobody stopped: the Panel app closed its scan socket (Android
// Bluetooth turned off and on, a failed scan renewal). The proxy starts it again, with its back-off.
var ScanLost hook.Hook[struct{}]

// PowerCycled fires when something has powered the controller off and on again (Secure Connections
// is set that way). Powering off ends the scan in the controller, while the scanner, which was never
// told, still holds it running: the proxy starts it again.
var PowerCycled hook.Hook[struct{}]

// Scanner is what the Bluetooth proxy drives: something that hears advertisements. The Dot's is
// the raw HCI Radio; the Show's goes through BlueZ (bluez.go), since bluetoothd owns the node.
type Scanner interface {
	// Start begins scanning (and advertising, where the scanner can) and calls found for every
	// advertisement heard, on the scanner's own goroutine.
	Start(scan, active bool, advertisement []byte, found func(Advertisement)) error
	Stop()
	Running() bool
	Scanning() bool
	Reports() uint64
}

var _ Scanner = (*Radio)(nil)
