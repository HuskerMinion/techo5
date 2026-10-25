//go:build !dot && !spot && !panel

package update

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// archSuffix is empty on the Show, whose binaries have always been published under the plain
// architecture name.
const archSuffix = ""

// onAthena and onSkylight are the layout's, variables so a test can stand on another board.
var (
	onAthena   = layout.Athena
	onSkylight = layout.Skylight
)

// rootfsArch is the key this device's root filesystem is published under in a manifest's Rootfs.
//
// The Show 5s and the 1st gen Show 8 share one daemon binary and one rootfs ("arm"): the same
// LineageOS kernel, the same partitions, the vendor tree in the store. The 2nd gen Show 8 (athena)
// shares the daemon binary but not the rootfs: it runs Amazon's own kernel from Amazon's boot image,
// takes its drivers from a vendor partition of its own and boots from a store laid out for that
// (tools/linux/athena). Another Show's rootfs would not come up there, and athena's would not on
// them, so athena's is keyed "arm-athena" and an athena never takes the plain "arm" one. A release
// carrying no athena rootfs leaves athenas where they are.
//
// The Skylight Calendar 15 is keyed the same way, "arm-skylight": the Show's arm daemon on TECHO5's
// mainline RK3566 kernel, booted from a store in what was Android's userdata. No other Show's rootfs
// may land on it, nor its on them.
func rootfsArch() string {
	switch {
	case onAthena():
		return arch + "-athena"
	case onSkylight():
		return arch + "-skylight"
	}
	return arch
}
