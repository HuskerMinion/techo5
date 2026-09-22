//go:build spot

package lenscover

// The Echo Spot has no shutter, and nothing on it advertises SW_CAMERA_LENS_COVER: its input nodes
// report no switches at all (measured on a unit 2026-09-22). See platform_cronos.go for why this is
// a board question rather than a capability one.
func hasShutter() bool { return false }
