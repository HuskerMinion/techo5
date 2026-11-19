//go:build !dot && !spot && !panel

package update

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The Lenovo ThinkSmart View's root filesystem is keyed "arm-thinksmart": the Show's arm daemon on
// the msm8953 mainline kernel. No other Show's rootfs may land on it, nor its on them, and a release
// carrying none leaves a ThinkSmart View where it is.
func init() {
	if layout.ThinkSmart() {
		boardRootfs = "thinksmart"
	}
}
