//go:build dot

package firstrun

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// A Dot, an Echo 2nd gen and a Dot 3rd gen have no screen: their first run is the setup page's, and
// one installed the usual way and added to Home Assistant is set up (doneByHome).
func init() {
	screenless = true
	// Improv has been tried on the Dot 2nd gen; the Dot 3rd gen's Bluetooth is a bridge of its own, and
	// is not reset for someone else until Improv is tried there.
	improvUntried = layout.Donut
}
