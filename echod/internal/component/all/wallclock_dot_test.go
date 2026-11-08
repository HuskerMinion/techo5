//go:build dot

package all

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The Echo Wall Clock needs RFCOMM, which only the Dot 3rd gen's kernel has (feature/wallclock).
func init() {
	if !layout.Donut() {
		notOnThisDevice = append(notOnThisDevice, "wall_clock", "wall_clock_pair", "wall_clock_status")
	}
}
