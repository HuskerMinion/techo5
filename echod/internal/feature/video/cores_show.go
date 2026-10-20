//go:build !dot && !spot

package video

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// What the boards with H.265, VP8 and VP9 in their ffmpeg (the 64-bit images, build-ffmpeg-arm64.sh)
// decode in time on their cores, through the player's own command (scaled, turned, made the panel's
// pixels); coresMost holds every other board to 1080p30.
//
// The 2nd gen Show 5's four Cortex-A53s at 1.3 GHz: VP9 1080p30 at 40 frames a second flat out and
// 200 s of it at real time in 199.6 s at about 2 cores, 720p60 at 83; 1080p60 fell behind, 20 s of it in
// 21.9 s. VP8 is cheaper but held to the same. H.265 720p30 kept up at 2.3 cores for the whole unit;
// 1080p30 did not (22.9 frames a second at 3.7 cores), so H.265 is held to 720p30 there.
//
// The 2nd gen Show 8's cores decode VP9 1080p at 128 frames a second: no limit on VP8 and VP9. Its
// H.265 has not been timed, so it keeps the 1080p30 every board has.
func init() {
	switch {
	case layout.CronosMainline():
		coresMost["hevc"] = p720 * 30
	case layout.AthenaMainline():
		delete(coresMost, "vp8")
		delete(coresMost, "vp9")
	}
}
