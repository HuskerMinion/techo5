//go:build !dot && !spot

package display

import (
	"image/color"

	"github.com/HuskerMinion/techo5/echod/internal/feature/babymon"
	"github.com/HuskerMinion/techo5/echod/internal/feature/camstream"
)

// colStreaming is the dot that says the camera is being streamed: the Spot's camera-on green.
var colStreaming = color.RGBA{60, 203, 127, 255}

// colRoomHeard is the dot that says the room is being listened to: amber, as a Dot's ring is.
var colRoomHeard = color.RGBA{0xE0, 0x8A, 0x10, 255}

// streamingDot is drawn in the top right corner, over everything, while camera streaming (feature/camstream)
// is sending the camera to somebody: a recorder can watch all day, and the room should be able to tell.
// An amber dot next to it says the room's sound is going to a browser (feature/babymon).
func (r *renderer) streamingDot(s scene) {
	x, y := r.w-r.s(20), r.s(20)
	for _, c := range []struct {
		on  bool
		col color.RGBA
	}{{s.streaming, colStreaming}, {s.listening, roomHeard(colRoomHeard)}} {
		if !c.on {
			continue
		}
		r.disc(x, y, r.s(10), color.RGBA{0, 0, 0, 160})
		r.disc(x, y, r.s(7), c.col)
		x -= r.s(26)
	}
}

// watchedDark is the frame of a dark panel while something is sent out of the room: black, and the dots.
func (r *renderer) watchedDark() {
	fillCanvas(r.dst, color.Black)
	r.streamingDot(scene{streaming: camstream.Watching(), listening: babymon.Shown()})
}
