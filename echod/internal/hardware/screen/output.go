//go:build !dot && !panel

package screen

import (
	"errors"
	"image"
	"log/slog"
)

// Outputs. A board whose panel is better reached another way than the framebuffer (DRM/KMS's own
// buffers flipped at vblank, say) registers an output from a file of its own (registerOutput, in an
// init). Open tries the outputs first, in the order registered, and the framebuffer after them; the
// Device an output opens has its pages through it (Device.out). Nothing here knows any board: with
// none registered, the panel is the framebuffer as ever.

// output is a way to the panel's pages other than the framebuffer's memory.
type output interface {
	// page is page i's memory, once it is free to draw on (no longer on show, nor about to be).
	page(i int) ([]byte, error)
	// show puts page i on the panel.
	show(i int) error
	// present puts img on page i (dst, its memory) in a way of the output's own, and says whether it
	// did; false leaves it to the package's row by row copy.
	present(d *Device, img *image.RGBA, dst []byte, i int) bool
	// drawing is the pages about to show something of their own: anything the output put over them
	// (a video on a plane of its own) goes.
	drawing()
	// leave hands the panel back, the frame on show kept on it where it can, and closes the output.
	leave(d *Device)
	String() string
}

// outputOpener opens a board's output: a Device whose out is it, or errNoOutput where this device
// does not have it.
type outputOpener struct {
	name string
	open func() (*Device, error)
}

var outputs []outputOpener

// errNoOutput is an output this device does not have: Open goes on to the next without a word.
var errNoOutput = errors.New("screen: no such output here")

// registerOutput adds a board's output, called from an init in the board's own file. No board in this tree
// registers one, so the panel is the framebuffer as ever; the hook is the seam a board build adds its own to.
//
//lint:ignore U1000 the extension point boards register their output through; unused in this tree by design.
func registerOutput(o outputOpener) { outputs = append(outputs, o) }

// openOutput is the first registered output that opens, nil for the framebuffer.
func openOutput() *Device {
	for _, o := range outputs {
		d, err := o.open()
		if err == nil {
			return d
		}
		if !errors.Is(err, errNoOutput) {
			slog.Warn("screen: an output did not open; trying the next", "output", o.name, "err", err)
		}
	}
	return nil
}

// pageFor is page next's memory to draw on: the output's, or the framebuffer's.
func (d *Device) pageFor(next int) ([]byte, error) {
	if d.out != nil {
		d.out.drawing()
		return d.out.page(next)
	}
	return d.mem[next*d.pageBytes : (next+1)*d.pageBytes], nil
}
