//go:build !dot

package screen

import (
	"bytes"
	"image"
)

// rowFilter is a filter laid over every row on its way to the panel (SetFilter), on the Echo's
// framebuffer and the app's surface alike: the canvas keeps what was drawn, and filtered what the
// panel is sent. prev is the canvas as it was last filtered, so only rows that changed are filtered
// again.
type rowFilter struct {
	f        func(row []byte, y int)
	name     string
	filtered *image.RGBA
	prev     []byte
}

// set lays f over the rows from the next frame; nil takes it off. A name already set is left as it
// is; a new one filters every row again.
func (rf *rowFilter) set(name string, f func(row []byte, y int)) {
	if name == rf.name {
		return
	}
	rf.f, rf.name, rf.prev = f, name, nil
	if f == nil {
		rf.filtered = nil
	}
}

// over is what to send for canvas c: c itself, or c through the filter.
func (rf *rowFilter) over(c *image.RGBA) *image.RGBA {
	if rf.f == nil {
		return c
	}
	if rf.filtered == nil || rf.filtered.Rect != c.Rect {
		rf.filtered, rf.prev = image.NewRGBA(c.Rect), nil
	}
	if rf.prev == nil {
		rf.prev = make([]byte, len(c.Pix))
		for i := range rf.prev {
			rf.prev[i] = ^c.Pix[i] // differs from every row, so the first frame filters them all
		}
	}
	w := c.Rect.Dx() * 4
	for y := 0; y < c.Rect.Dy(); y++ {
		at := y * c.Stride
		row := c.Pix[at : at+w]
		if bytes.Equal(row, rf.prev[at:at+w]) {
			continue
		}
		copy(rf.prev[at:at+w], row)
		out := rf.filtered.Pix[at : at+w]
		copy(out, row)
		rf.f(out, y)
	}
	return rf.filtered
}
