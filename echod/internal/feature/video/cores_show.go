//go:build !dot && !spot

package video

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// What the boards with H.265, VP8 and VP9 in their ffmpeg (build-ffmpeg-arm64.sh) decode in time on
// their cores, through the player's own command (scaled, turned, made the panel's pixels); coresMost
// holds every other board to 1080p30. The images give that build to athena and the Skylight, on their
// 64-bit kernels.
//
// The 2nd gen Show 5's four Cortex-A53s at 1.3 GHz: VP9 1080p30 at 40 frames a second flat out and
// 200 s of it at real time in 199.6 s at about 2 cores, 720p60 at 83; 1080p60 fell behind, 20 s of it in
// 21.9 s. VP8 is cheaper but held to the same. H.265 720p30 kept up at 2.3 cores for the whole unit;
// 1080p30 did not (22.9 frames a second at 3.7 cores), so H.265 is held to 720p30 there.
//
// The 2nd gen Show 8's cores decode VP9 1080p at 128 frames a second: VP8 and VP9 are held to 1080p120,
// so a 4K file is still refused. Its H.265 has not been timed, so it keeps the 1080p30 every board has.
//
// The Skylight's RK3566 has four Cortex-A55s at up to 1.8 GHz. Decoding alone, H.265 1080p30 ran at 71
// frames a second flat out and at about 1.4 cores at real time, VP9 1080p30 at 85 and about 1.1 cores; a
// 1080p camera's H.265 at 14 frames a second took about 0.6 of a core. Its screen is 1080p, so the
// player's scaling adds little: it keeps the 1080p30 every board has for all three.
func init() {
	switch {
	case layout.CronosMainline():
		coresMost["hevc"] = p720 * 30
	case layout.AthenaMainline():
		coresMost["vp8"], coresMost["vp9"] = p1080*120, p1080*120
	}
}
