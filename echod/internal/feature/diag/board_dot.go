//go:build dot

package diag

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// radioHere and luxHere are whether the board has a radio temperature and a room brightness to read.
// The Echo Dot 3's Wi-Fi chip reports no temperature and it has no light sensor, so neither is put up
// there: an entity that can never have a value looks like a fault.
func radioHere() bool { return !layout.Donut() }
func luxHere() bool   { return !layout.Donut() }
