//go:build panel

package diag

// adbHere is false on a panel: adb on a tablet is Android's own (Developer options), and the app has
// no firewall to open it in, so the switch would do nothing.
const adbHere = false
