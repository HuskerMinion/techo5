//go:build spot

package diag

// radioHere and luxHere are whether the board has a radio temperature and a room brightness to read.
// The Spot's Wi-Fi chip reports no temperature; its light sensor is read through the screen's
// auto-brightness (roomLux).
func radioHere() bool { return false }
func luxHere() bool   { return true }
