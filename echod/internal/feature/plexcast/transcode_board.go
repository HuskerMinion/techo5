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
func init() { capProfile = profileFor(layout.Skylight()) }

// profileFor is what a board's own screen and decoder take: the Skylight's 1920 by 1080, and 720p for
// every other screen here -- the Shows' 960 by 480 and 1280 by 800, the Spot's 480 by 480. It is a
// function rather than the switch above so that both answers are covered by a test, which also checks
// that the profile this build runs with is the one its board asks for (plexcast_test.go).
func profileFor(skylight bool) string {
	if skylight {
		return capProfileAt(1920, 1080)
	}
	return capProfileAt(1280, 720)
}
