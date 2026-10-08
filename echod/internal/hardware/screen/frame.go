//go:build !dot

package screen

import (
	"fmt"
	"image"
)

// A frame is a picture made somewhere else already in the panel's own shape and byte order — a video
// frame from the decoder — so it is copied to the panel as it is rather than drawn on the canvas and
// turned: one panel row after another, FrameSize's width of four-byte pixels each, laid out as PixFmt
// says.

// FrameSize is a frame's size: the panel's, 480×960 on the Show (portrait: a frame has already been
// turned for it), 480×480 on the Spot.
func (d *Device) FrameSize() (w, h int) { return d.panelW, d.panelH }

// Rotated is whether a landscape picture has to be turned a quarter turn clockwise to become a frame.
func (d *Device) Rotated() bool { return rotated }

// PixFmt is ffmpeg's name for the panel's pixel layout, or "" for a layout it has no name for.
func (d *Device) PixFmt() string { return pixFmt(d.shift) }

func pixFmt(shift [4]uint) string {
	switch shift {
	case [4]uint{16, 8, 0, 24}:
		return "bgra"
	case [4]uint{0, 8, 16, 24}:
		return "rgba"
	}
	return ""
}

// PresentFrame paints frame onto the page not on show and pans to it, with the canvas's rectangle
// over drawn over it, blended by its alpha (the video page's controls). An empty rectangle is the
// frame alone.
func (d *Device) PresentFrame(frame []byte, over image.Rectangle) error {
	next := d.page
	if d.pages > 1 {
		next = (d.page + 1) % d.pages
	}
	if err := d.paintFrame(d.mem[next*d.pageBytes:(next+1)*d.pageBytes], next, frame, over); err != nil {
		return err
	}
	return d.pan(next)
}

// paintFrame writes frame and the canvas's over onto page next. The page no longer holds what its
// shadow says, so the next Present onto it writes every row.
func (d *Device) paintFrame(dst []byte, next int, frame []byte, over image.Rectangle) error {
	rowBytes := d.panelW * 4
	if len(frame) < rowBytes*d.panelH {
		return fmt.Errorf("screen: a frame of %d bytes, want %d", len(frame), rowBytes*d.panelH)
	}
	if pixFmt(d.shift) == "" {
		return fmt.Errorf("screen: no frame layout for this panel")
	}
	d.markStale(next)
	over = over.Intersect(d.canvas.Rect)
	if over.Empty() && d.line == rowBytes {
		copy(dst[:rowBytes*d.panelH], frame)
		return nil
	}
	if len(d.row) < rowBytes {
		d.row = make([]byte, rowBytes)
	}
	row := d.row[:rowBytes]
	for py := 0; py < d.panelH; py++ {
		src := frame[py*rowBytes : (py+1)*rowBytes]
		out := dst[py*d.line : py*d.line+rowBytes]
		if !d.overRow(over, py) {
			copy(out, src)
			continue
		}
		// Blended in RAM and written whole: the framebuffer's own memory is slow to read back.
		copy(row, src)
		d.blendRow(row, py, over)
		copy(out, row)
	}
	return nil
}

// overRow is whether panel row py has any of the canvas's rectangle over on it.
func (d *Device) overRow(over image.Rectangle, py int) bool {
	if over.Empty() {
		return false
	}
	if rotated {
		return py >= over.Min.X && py < over.Max.X // a panel row is a canvas column
	}
	return py >= over.Min.Y && py < over.Max.Y
}

// blendRow draws the canvas's over onto panel row py, held in row. The canvas is premultiplied, so a
// pixel is its own color plus what is under it scaled by what it leaves.
func (d *Device) blendRow(row []byte, py int, over image.Rectangle) {
	img := d.canvas
	ri, gi, bi := d.shift[0]/8, d.shift[1]/8, d.shift[2]/8
	blend := func(at int, p []byte) {
		a := uint32(p[3])
		if a == 0 {
			return
		}
		keep := 255 - a
		row[at+int(ri)] = uint8(uint32(p[0]) + uint32(row[at+int(ri)])*keep/255)
		row[at+int(gi)] = uint8(uint32(p[1]) + uint32(row[at+int(gi)])*keep/255)
		row[at+int(bi)] = uint8(uint32(p[2]) + uint32(row[at+int(bi)])*keep/255)
	}
	if rotated {
		// Canvas (x, y) is panel (panelW-1-y, x): this row is canvas column py.
		x := py
		for y := over.Min.Y; y < over.Max.Y && y < d.panelW; y++ {
			i := y*img.Stride + x*4
			blend((d.panelW-1-y)*4, img.Pix[i:i+4:i+4])
		}
		return
	}
	y := py
	for x := over.Min.X; x < over.Max.X && x < d.panelW; x++ {
		i := y*img.Stride + x*4
		blend(x*4, img.Pix[i:i+4:i+4])
	}
}

// markStale says page next holds something other than its shadow.
func (d *Device) markStale(next int) {
	if len(d.stale) < d.pages {
		d.stale = make([]bool, max(d.pages, 1))
	}
	d.stale[next] = true
}

// takeStale is whether page next has to be written whole, and clears it.
func (d *Device) takeStale(next int) bool {
	if next >= len(d.stale) || !d.stale[next] {
		return false
	}
	d.stale[next] = false
	return true
}
