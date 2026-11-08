//go:build !dot

package wallclock

// boardHasRFCOMM is whether this board's kernel can open the clock's Bluetooth Classic link: every
// Show, Spot and Skylight kernel has RFCOMM built in.
func boardHasRFCOMM() bool { return true }
