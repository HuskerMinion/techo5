//go:build panel

package touch

// TECHO5 Panel: the app sends touches already in the canvas's own frame (lib/panelscreen), so the
// raw frame is the canvas and there is nothing to turn. The sizes are the app's, known once it has
// said hello, so they are variables set at Start; until then they are a Show 8's.
var (
	// Width and Height are the frame coordinates are reported in.
	Width  = 1280
	Height = 800

	// tapMove and notch are the Show 5's 24 and 40 at its 960 width, scaled to the canvas's.
	tapMove = 32
	notch   = 56
)

const (
	// holdGestures: like the Show, taps and swipes only.
	holdGestures = false

	verticalOnly = false
)

// scaleTo sets the sizes for a canvas w wide and h tall.
func scaleTo(w, h int) {
	Width, Height = w, h
	tapMove = 24 * w / 960
	notch = 40 * w / 960
}

func toFrame(_, _, rx, ry int) (x, y int) { return rx, ry }
