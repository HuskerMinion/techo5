//go:build !dot && !spot && !panel

package screen

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The ThinkSmart View's /dev/fb0 is msm DRM's fbdev emulation ("msmdrmfb"): 800 by 1280, 32 bits, one
// page, shown once unblanked. Its backlight is the device tree's "backlight" node, 0 to 4095 on the
// units read 2026-11-19; the top is read from the driver.
const (
	thinkSmartBacklightDir = "/sys/class/backlight/backlight"
	thinkSmartBacklightMax = 4095
)

func init() {
	if !layout.ThinkSmart() {
		return
	}
	unblankOnOpen = true
	singlePage = true
	boardBacklight = func() (string, int, bool) {
		return thinkSmartBacklightDir + "/brightness", readTop(thinkSmartBacklightDir+"/max_brightness", thinkSmartBacklightMax), true
	}
}
