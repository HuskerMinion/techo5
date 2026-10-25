//go:build panel

package buttons

// Here is false on a panel: a tablet's keys are Android's, an app cannot read the input devices, and
// the app hands the daemon none. Nothing under /dev/input is looked at, because a tablet on the Echo
// Show 5's MediaTek chip (the 8th gen Fire HD 8) reports the same volume key codes and would be read
// as a Show's buttons wherever the nodes could be opened.
const Here = false

// longHoldAfter is zero: there are no buttons to hold.
const longHoldAfter = 0

// codes is empty: no key is a button here.
var codes map[uint16]Name

// notButtons: no input device is read at all.
var notButtons map[string]bool
