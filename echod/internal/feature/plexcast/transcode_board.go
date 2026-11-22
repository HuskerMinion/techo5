//go:build !dot

package plexcast

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The panel decides the cap (transcode.go). Every device here but one is a Show or a Spot, whose screens
// are 960 by 480, 1280 by 800 and 480 by 480: a 1080p picture would be scaled away by the player before
// it reached the glass, and the software decoder wants all four cores for it (feature/video, threads).
// The Skylight Calendar's 1920 by 1080 and its RK3566 take it -- H.264 costs it less than the H.265 the
// player already allows there -- so the server is asked for 1080p on that board alone.
//
// This is where a future board with a better decoder adds itself, as cores_show.go adds one to
// feature/video's limits: the profile itself is written once, in capProfileAt.
func init() {
	if layout.Skylight() {
		capProfile = capProfileAt(1920, 1080)
	}
}
