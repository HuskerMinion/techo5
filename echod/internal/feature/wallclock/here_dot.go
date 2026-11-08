//go:build dot

package wallclock

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// boardHasRFCOMM is whether this board's kernel can open the clock's Bluetooth Classic link. The Dot
// 3rd gen's 6.1 kernel can; the Echo Dot 2nd gen and the Echo 2nd gen run their stock kernel with
// Bluetooth added as modules, which leave RFCOMM out, so the clock is not offered there.
func boardHasRFCOMM() bool { return layout.Donut() }
