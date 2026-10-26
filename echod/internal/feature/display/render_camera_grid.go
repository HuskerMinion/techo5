//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The camera grid: the cameras' snapshots in cells, each with its name, the time at the top as on a
// camera, and at the foot what a tap does. A tap on a cell puts that camera up on its own (cameraTap); a
// tap on the strips closes the grid.

// gridCells is where the cells of a grid of n go on a panel of w by h, between the strips.
func (r *renderer) gridCells(n int) []image.Rectangle {
	return gridRects(n, image.Rect(0, r.gridTop(), r.w, r.h-r.cameraStrip()), r.s(4))
}

// gridTop is where the cells start, under the strip with the name and the time.
func (r *renderer) gridTop() int { return 44 }

// gridRects lays n cells out in area, cols by rows as home.GridLayout says, gap apart.
func gridRects(n int, area image.Rectangle, gap int) []image.Rectangle {
	if n <= 0 {
		return nil
	}
	cols, rows := home.GridLayout(n)
	cw := (area.Dx() - gap*(cols-1)) / cols
	ch := (area.Dy() - gap*(rows-1)) / rows
	out := make([]image.Rectangle, 0, n)
	for i := range n {
		col, row := i%cols, i/cols
		x := area.Min.X + col*(cw+gap)
		y := area.Min.Y + row*(ch+gap)
		out = append(out, image.Rect(x, y, x+cw, y+ch))
	}
	return out
}

// gridCellAt is the cell of a grid of n that a tap at p is for, or -1 for none: the cell it is in, or the
// nearest one for a tap in the gap between two. A tap on the strips is no cell.
func (r *renderer) gridCellAt(p image.Point, n int) int {
	if p.Y < r.gridTop() || p.Y >= r.h-r.cameraStrip() {
		return -1
	}
	best, bestD := -1, 0
	for i, c := range r.gridCells(n) {
		if p.In(c) {
			return i
		}
		dx := max(c.Min.X-p.X, 0, p.X-(c.Max.X-1))
		dy := max(c.Min.Y-p.Y, 0, p.Y-(c.Max.Y-1))
		if d := dx*dx + dy*dy; best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// fitted keeps each snapshot as fitted to its cell, so a picture is scaled once when it arrives rather
// than at every frame drawn: by the picture it was made from and the cell's size, and only for the
// pictures on the screen now.
var fitted struct {
	sync.Mutex
	m map[fitKey]*image.RGBA
}

type fitKey struct {
	src  *image.RGBA
	w, h int
}

// fitCell is src fitted to a cell of w by h, keeping its shape.
func fitCell(src *image.RGBA, w, h int, keep map[fitKey]*image.RGBA) *image.RGBA {
	k := fitKey{src, w, h}
	fitted.Lock()
	out, ok := fitted.m[k]
	fitted.Unlock()
	if !ok {
		if b := src.Bounds(); b.Dx() == w || b.Dy() == h {
			if b.Dx() <= w && b.Dy() <= h {
				out = src // already the cell's size, one way or the other
			}
		}
		if out == nil {
			home.ReadFrame(func() { out = home.FitInto(src, w, h) })
		}
	}
	keep[k] = out
	return out
}

func (r *renderer) cameraGrid(s scene, v home.CameraView) {
	r.setCameraTalkAt(image.Rectangle{})
	r.setCameraSoundAt(image.Rectangle{})
	cells := r.gridCells(len(v.Grid))
	keep := map[fitKey]*image.RGBA{}
	defer func() {
		fitted.Lock()
		fitted.m = keep
		fitted.Unlock()
	}()
	for i, c := range v.Grid {
		box := cells[i]
		name := c.Name
		if s.demo {
			name = demoCameras[i%len(demoCameras)]
		}
		if c.Frame != nil {
			pic := fitCell(c.Frame, box.Dx(), box.Dy(), keep)
			b := pic.Bounds()
			x := box.Min.X + (box.Dx()-b.Dx())/2
			y := box.Min.Y + (box.Dy()-b.Dy())/2
			draw.Draw(r.dst, image.Rect(x, y, x+b.Dx(), y+b.Dy()), pic, b.Min, draw.Src)
		} else {
			msg := tr("drawer_connecting")
			if c.Error != "" {
				msg = c.Error
			}
			msg = r.clipTo(r.tiny, msg, box.Dx()-2*r.s(8))
			r.text(r.tiny, msg, box.Min.X+(box.Dx()-r.width(r.tiny, msg))/2, box.Min.Y+box.Dy()/2, dim)
		}
		// The name on a dark strip at the foot of its cell, so it reads over any picture.
		label := image.Rect(box.Min.X, box.Max.Y-r.s(26), box.Max.X, box.Max.Y)
		draw.Draw(r.dst, label, image.NewUniform(shade), image.Point{}, draw.Over)
		r.text(r.tiny, r.clipTo(r.tiny, name, box.Dx()-2*r.s(8)), box.Min.X+r.s(8), box.Max.Y-r.s(8), captionText)
	}

	draw.Draw(r.dst, image.Rect(0, 0, r.w, r.gridTop()), image.NewUniform(shade), image.Point{}, draw.Over)
	r.text(r.small, tr("cameras"), r.margin, 32, captionText)
	t := clockHM(s.now)
	r.text(r.small, t, r.w-r.margin-r.width(r.small, t), 32, captionDim)

	hint := tr("drawer_grid_close")
	if left := time.Until(v.Until).Round(time.Second); left > 0 && left < 24*time.Hour {
		hint += "  ·  " + left.String()
	}
	draw.Draw(r.dst, image.Rect(0, r.h-r.cameraStrip(), r.w, r.h), image.NewUniform(shade), image.Point{}, draw.Over)
	r.text(r.tiny, r.clipTo(r.tiny, hint, r.w-2*r.margin), r.margin, r.h-11, captionDim)
}

// gridCellAt is the grid cell a tap at p landed on, of a grid of n, or -1.
func (d *Display) gridCellAt(p image.Point, n int) int {
	if d.r == nil {
		return -1
	}
	return d.r.gridCellAt(p, n)
}
