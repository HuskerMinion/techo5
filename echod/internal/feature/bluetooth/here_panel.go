//go:build panel

package bluetooth

// Here: a panel has no proxy. Android's Bluetooth stack owns the tablet's radio and an app has no BlueZ
// to scan with, and a proxy that never scans is worse than none: Home Assistant takes a scanner that
// does not run for a fault and asks for the device to be power cycled (Features). So nothing is
// registered or advertised.
const Here = false
