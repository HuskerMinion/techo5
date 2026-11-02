//go:build !dot && !spot

package diag

// radioHere and luxHere are whether the board has a radio temperature and a room brightness to read.
func radioHere() bool { return true }
func luxHere() bool   { return true }
