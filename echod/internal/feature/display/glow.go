//go:build !dot

package display

import (
	"image"
	"image/color"
	"math"
	"time"
)

// The Glow clock style's colors: a few soft blobs drifting slowly over the ground, worked out on a
// coarse grid (glowCell panel pixels to a point) and stretched smooth over the screen, so a frame
// costs little more than the stretch.

// glowCell is how many panel pixels apart the field's points are.
const glowCell = 8

// glowBlobs are the blobs' paths: each goes round a Lissajous figure over the screen, slowly enough
// (minutes a turn) that a frame a second looks like a drift.
var glowBlobs = [...]struct {
	periodX, periodY float64 // seconds
	phase            float64
	size             float64 // radius, as a part of the screen's height
	color            int     // which of the colors
}{
	{97, 131, 0.0, 0.42, 0},
	{151, 89, 1.7, 0.36, 1},
	{173, 113, 3.1, 0.40, 2},
	{211, 157, 4.4, 0.30, 0},
}

// glowBuffers are a renderer's field for the Glow style and a row of the stretch, kept from frame to
// frame, as a frame a second of a few megabytes each would keep the collector busy on the device.
type glowBuffers struct {
	field *image.RGBA
	row   []uint32
}

// sized is img when it is w by h, else a new image that is.
func sized(img *image.RGBA, w, h int) *image.RGBA {
	if img == nil || img.Rect.Dx() != w || img.Rect.Dy() != h {
		return image.NewRGBA(image.Rect(0, 0, w, h))
	}
	return img
}

// stretch draws the field over all of dst, its points glowCell pixels apart from dst's corner and
// blended straight between, inside mask where there is one (the same size as dst) and as much as it
// lets through. It is the bilinear stretch done for a whole-number scale, a row at a time, with none
// of a general scaler's work per pixel: it is most of what a Glow frame costs.
func (g *glowBuffers) stretch(dst *image.RGBA, field *image.RGBA, mask *image.Alpha) {
	const cell, shift = glowCell, 6 // a cell's area is 1<<shift
	fw, fh := field.Rect.Dx(), field.Rect.Dy()
	if cap(g.row) < 3*fw {
		g.row = make([]uint32, 3*fw)
	}
	row := g.row[:3*fw]
	b := dst.Rect
	for y := range b.Dy() {
		// The two rows of points either side of this line, mixed for it.
		fy, ty := min(y/cell, fh-1), uint32(y%cell)
		fy1 := min(fy+1, fh-1)
		p0, p1 := field.Pix[fy*field.Stride:], field.Pix[fy1*field.Stride:]
		for i := range fw {
			for c := range 3 {
				row[3*i+c] = uint32(p0[4*i+c])*(cell-ty) + uint32(p1[4*i+c])*ty
			}
		}
		out := dst.Pix[y*dst.Stride:]
		var m []uint8
		if mask != nil {
			m = mask.Pix[y*mask.Stride:]
		}
		// Then along the line, a cell at a time: from one point's color toward the next one's. The steps
		// are worked in wrapping unsigned sums; each pixel's own sum is never below nought.
		for fx := 0; fx < fw && fx*cell < b.Dx(); fx++ {
			fx1 := min(fx+1, fw-1)
			r, gr, bl := row[3*fx]*cell, row[3*fx+1]*cell, row[3*fx+2]*cell
			dr, dg, db := row[3*fx1]-row[3*fx], row[3*fx1+1]-row[3*fx+1], row[3*fx1+2]-row[3*fx+2]
			x0 := fx * cell
			n := min(cell, b.Dx()-x0)
			px := out[4*x0 : 4*(x0+n)]
			if m == nil {
				for o := 0; o+3 < len(px); o += 4 {
					px[o], px[o+1], px[o+2], px[o+3] = uint8(r>>shift), uint8(gr>>shift), uint8(bl>>shift), 255
					r, gr, bl = r+dr, gr+dg, bl+db
				}
				continue
			}
			mk := m[x0 : x0+n]
			for i, k := range mk {
				o := 4 * i
				cr, cg, cb := r>>shift, gr>>shift, bl>>shift
				r, gr, bl = r+dr, gr+dg, bl+db
				if k == 0 {
					continue
				}
				if k != 255 {
					kk := uint32(k)
					cr = (cr*kk + uint32(px[o])*(255-kk)) / 255
					cg = (cg*kk + uint32(px[o+1])*(255-kk)) / 255
					cb = (cb*kk + uint32(px[o+2])*(255-kk)) / 255
				}
				px[o], px[o+1], px[o+2], px[o+3] = uint8(cr), uint8(cg), uint8(cb), 255
			}
		}
	}
}

// glowField is the field at time now, w by h points, in ground with the three colors mixed in, drawn
// into the field kept in g.
func (g *glowBuffers) glowField(w, h int, now time.Time, ground color.RGBA, colors [3]color.RGBA) *image.RGBA {
	g.field = sized(g.field, w, h)
	img := g.field
	t := float64(now.UnixMilli()) / 1000
	type blob struct{ x, y, r2 float64 }
	var blobs [len(glowBlobs)]blob
	fw, fh := float64(w), float64(h)
	for i, b := range glowBlobs {
		x := 0.5 + 0.38*math.Sin(2*math.Pi*t/b.periodX+b.phase)
		y := 0.5 + 0.36*math.Sin(2*math.Pi*t/b.periodY+b.phase*1.3)
		r := b.size * fh
		blobs[i] = blob{x * fw, y * fh, r * r}
	}
	for py := range h {
		for px := range w {
			var weight [3]float64
			total := 0.0
			for i, b := range blobs {
				dx, dy := float64(px)-b.x, float64(py)-b.y
				v := b.r2 / (dx*dx + dy*dy + b.r2*0.25)
				weight[glowBlobs[i].color] += v
				total += v
			}
			// How much of the colors shows: none far from every blob, up to most of it in one.
			k := 0.5 * smoothstep(0.35, 1.6, total)
			var c [3]float64
			for i, col := range colors {
				share := weight[i] / total
				c[0] += share * float64(col.R)
				c[1] += share * float64(col.G)
				c[2] += share * float64(col.B)
			}
			o := img.PixOffset(px, py)
			img.Pix[o+0] = uint8(float64(ground.R)*(1-k) + c[0]*k)
			img.Pix[o+1] = uint8(float64(ground.G)*(1-k) + c[1]*k)
			img.Pix[o+2] = uint8(float64(ground.B)*(1-k) + c[2]*k)
			img.Pix[o+3] = 255
		}
	}
	return img
}

// smoothstep is 0 below a, 1 above b, and an easing curve between.
func smoothstep(a, b, x float64) float64 {
	t := math.Min(math.Max((x-a)/(b-a), 0), 1)
	return t * t * (3 - 2*t)
}
