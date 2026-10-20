//go:build !dot && !spot

package video

import "github.com/HuskerMinion/techo5/echod/internal/layout"

// The 2nd gen Show 5's four Cortex-A53s decode VP9 1080p30 in time, and no more: through the player's
// own command (scaled, turned, made the panel's pixels), 1080p30 at 40 frames a second flat out and 200 s
// of it at real time in 199.6 s at about 2 cores, 720p60 at 83; 1080p60 fell behind, 20 s of it in
// 21.9 s (custom-kernel/cronos/research/mt8163-video-codec.md, "VP8 and VP9 on the cores"). VP8 is
// cheaper but held to the same. The Show 8's cores decode VP9 1080p at 128 frames a second
// (custom-kernel/athena/research/vdec-vp8-vp9.md): no limit there.
func init() {
	if layout.CronosMainline() {
		vpxMost = 1920 * 1088 * 30
	}
}
