package rainbow

import "math"

// The screen in design units: 640 across, with its corners rounded; the glow
// reaching maxD in from the edge, fading out from fade0; and the edge's sharp part, the line along it
// and the rim, within nearD: the line is at most 10.5 wide.
const (
	designW = 640.0
	cornerR = 38.0
	maxD    = 110.0
	fade0   = 55.0
	nearD   = 16.0
	farD    = 24.0 // beyond it, the line's glow and the sharp part of the light have gone
)

// edgeCell is one cell of a grid near the edge: where it is in the grid, how far in from the edge it
// is, where along the edge (0 to 1, round from the top left corner), how much it belongs to the top
// and bottom (ny) and to the right (nx), how much of the glow reaches it (wn), and the sharp part of
// the glow there (near), which only depends on how far in it is.
//
// The rest is what the light there is worked out from each frame, also fixed for the cell: its step
// along the edge in the frame's tables (ui), how much of the light it gets listening and thinking (wEn,
// wEt) and from the entry's push (nx75), how far the light reaches in at it (rA with the voice, rT
// thinking, rB with the push), and whether it is far enough in for the glow alone to reach it (far).
type edgeCell struct {
	at                     int32
	d, u, ny, nx, wn, near float32
	wEn, wEt, nx75         float32
	rA, rT, rB             float32
	ui                     uint16
	far                    bool
}

// cornerPixel is a pixel of a rounded corner: its offset in the screen's pixels, and how much of it is
// inside the rounding, of 256.
type cornerPixel struct {
	at   int32
	keep uint16
}

// geometry is a screen size's edge, worked out once: the soft glow's grid at a quarter of the size,
// the sharp edge's at half, the run of each half row in the middle that the light never reaches, and
// the corners' pixels.
type geometry struct {
	w, h   int
	k      float64 // pixels per design unit
	wd, hd float64 // the screen in design units
	per    perimeter

	qw, qh int
	soft   []edgeCell
	hw, hh int
	sharp  []edgeCell
	inner  [][2]int32 // per half row: [from, to) of cells left dark
	soft2  [][2]int32 // per half row: [from, to) of cells the soft glow reaches beyond the sharp edge
	corner []cornerPixel
}

// perimeter is the rounded rectangle of the screen, in design units, and where each stretch of its
// edge starts along it, clockwise from the top left corner's end: top, top right corner, right, and
// so on round; p is the whole way round.
type perimeter struct {
	w, h, r                   float64
	s1, s2, s3, s4, s5, s6, p float64
}

func newPerimeter(w, h, r float64) perimeter {
	q := math.Pi * r / 2
	e := perimeter{w: w, h: h, r: r}
	e.s1 = w - 2*r
	e.s2 = e.s1 + q
	e.s3 = e.s2 + h - 2*r
	e.s4 = e.s3 + q
	e.s5 = e.s4 + w - 2*r
	e.s6 = e.s5 + q
	e.p = e.s6 + h - 2*r + q
	return e
}

// sdf is how far x, y is inside the edge: negative outside, as in a rounded corner's tip.
func (e *perimeter) sdf(x, y float64) float64 {
	r, w, h := e.r, e.w, e.h
	switch {
	case x < r && y < r:
		return r - math.Hypot(x-r, y-r)
	case x > w-r && y < r:
		return r - math.Hypot(x-w+r, y-r)
	case x > w-r && y > h-r:
		return r - math.Hypot(x-w+r, y-h+r)
	case x < r && y > h-r:
		return r - math.Hypot(x-r, y-h+r)
	}
	return min(y, h-y, x, w-x)
}

// at is where x, y sits along the edge, 0 to 1, and how much it belongs to the top and bottom and to
// the right. Each side's place along the edge is weighed by how near that side is, softly, so that
// going round a corner the place turns smoothly and the light has no seam there.
func (e *perimeter) at(x, y float64) (u, ny, nx float64) {
	r, w, h := e.r, e.w, e.h
	cx, cy := min(max(x, r), w-r), min(max(y, r), h-r)
	dt, db, dl, dr := y, h-y, x, w-x
	m := min(dt, db, dl, dr)
	tau := 6 + 0.7*max(0, m)
	weigh := func(d float64) float64 {
		if f := (d - m) / tau; f < 40 {
			return math.Exp(-f)
		}
		return 0
	}
	wt, wb, wl, wr := weigh(dt), weigh(db), weigh(dl), weigh(dr)
	sum := wt + wb + wl + wr
	var sx, sy float64
	add := func(wgt, along float64) {
		if wgt > 0 {
			a := 2 * math.Pi * along / e.p
			sx += wgt * math.Cos(a)
			sy += wgt * math.Sin(a)
		}
	}
	add(wt, cx-r)
	add(wr, e.s2+cy-r)
	add(wb, e.s4+w-r-cx)
	add(wl, e.s6+h-r-cy)
	u = math.Atan2(sy, sx) / (2 * math.Pi)
	if u < 0 {
		u++
	}
	return u, (wt + wb) / sum, wr / sum
}

// newGeometry works out the edge of a screen w by h pixels. A few hundred milliseconds on the Show,
// once.
func newGeometry(w, h int) *geometry {
	k := float64(w) / designW
	g := &geometry{w: w, h: h, k: k, wd: designW, hd: float64(h) / k}
	g.per = newPerimeter(g.wd, g.hd, cornerR)
	cell := func(at int, px, py float64, near bool) (edgeCell, bool) {
		x, y := px/k, py/k
		d := g.per.sdf(x, y)
		if d <= -1.5 {
			return edgeCell{}, false
		}
		if near && d >= nearD {
			return edgeCell{}, false
		}
		// The soft grid starts a little inside where the sharp one stops, so a half cell beyond it has
		// all four of its neighbors to be drawn from.
		if !near && (d >= maxD || d < nearD-2*4/k-1) {
			return edgeCell{}, false
		}
		u, ny, nx := g.per.at(x, y)
		q := clamp01((d - fade0) / (maxD - fade0))
		d = max(0, d)
		return edgeCell{
			at: int32(at), d: float32(d), u: float32(u), ny: float32(ny), nx: float32(nx),
			wn: float32(1 - q*q*(3-2*q)), near: float32(0.85 * math.Exp(-d/2.6)),
			wEn: float32(0.5 + 0.5*ny), wEt: float32(0.05 + 0.95*ny*ny), nx75: float32(0.75 * nx),
			rA: float32(24 * (0.4 + 0.6*ny)), rT: float32(12 + 34*ny), rB: float32(6 + 16*nx),
			ui: uint16(int(u*along) & (along - 1)), far: d >= farD,
		}, true
	}
	g.qw, g.qh = (w+3)/4, (h+3)/4
	for j := range g.qh {
		for i := range g.qw {
			if c, ok := cell(j*g.qw+i, float64(4*i+2), float64(4*j+2), false); ok {
				g.soft = append(g.soft, c)
			}
		}
	}
	g.hw, g.hh = (w+1)/2, (h+1)/2
	g.inner, g.soft2 = make([][2]int32, g.hh), make([][2]int32, g.hh)
	for j := range g.hh {
		from, to := int32(g.hw/2), int32(g.hw/2)
		first, last := int32(g.hw), int32(0)
		for i := range g.hw {
			if c, ok := cell(j*g.hw+i, float64(2*i+1), float64(2*j+1), true); ok {
				g.sharp = append(g.sharp, c)
			} else if g.per.sdf(float64(2*i+1)/k, float64(2*j+1)/k) >= nearD {
				first, last = min(first, int32(i)), int32(i+1)
			}
			if g.per.sdf(float64(2*i+1)/k, float64(2*j+1)/k) >= maxD {
				if from == to {
					from = int32(i)
				}
				to = int32(i + 1)
			}
		}
		g.inner[j] = [2]int32{from, to}
		g.soft2[j] = [2]int32{first, max(first, last)}
	}
	// The corners: the screen is drawn rounded while the light is on.
	rp := int(math.Ceil(cornerR*k)) + 1
	for y := range h {
		for x := range w {
			if (x >= rp && x < w-rp) || (y >= rp && y < h-rp) {
				continue
			}
			keep := clamp01(g.per.sdf((float64(x)+0.5)/k, (float64(y)+0.5)/k)*k + 0.5)
			if keep < 1 {
				g.corner = append(g.corner, cornerPixel{int32((y*w + x) * 4), uint16(keep*256 + 0.5)})
			}
		}
	}
	return g
}
