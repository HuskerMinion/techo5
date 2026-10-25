//go:build !dot && !spot

package lenscover

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// hasShutter says whether this board has a physical cover over the lens with a switch behind it. Both
// Echo Show 8s do: the 2nd gen's slider is GPIO152 on gpio-keys and reports covered only while closed
// (checked on a unit, 2026-10-19: covered=true on closing, false on opening; before it was trusted
// here, closing it while the camera ran turned the picture white, the exposure loop chasing a black
// frame). So does the 2nd gen Show 5 on a mainline kernel (GPIO 142 in its device tree, gpio-keys'
// SW_CAMERA_LENS_COVER: 1 with the slider across the lens, 0 open, read on a unit 2026-10-16). That is
// on TECHO5's own kernels; a 2nd gen on Amazon's kernel has no camera here and an unchecked switch.
//
// This has to be a board question, not a capability question. On their vendor kernels both Show 5
// generations expose
// SW_CAMERA_LENS_COVER on their gpio-keys node exactly as the Show 8 does — measured on a 2nd gen
// unit 2026-09-22, caps.sw=200, which is bit 9 — while having no shutter at all. The switch read
// false on that unit, so nothing was blocked, but reading the capability bitmap alone would publish
// a cover sensor for hardware that has none, and a unit whose pin read the other way would refuse
// the camera with nothing in the log but "the lens cover is closed".
func hasShutter() bool { return layout.Crown() || layout.AthenaMainline() || layout.CronosMainline() }
