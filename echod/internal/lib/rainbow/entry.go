package rainbow

import (
	"image"
	"math"
	"sync/atomic"
)

// The entry: a wave of pink comes in from the right edge, bulging in the middle, and crosses the
// screen; round its front the page is pushed aside and rippled, and behind it the page is frosted,
// richer in color and tinted warm to violet; a gold globe swells at the right edge where it starts and
// fades as the wave goes; the page draws back a little and settles, and the frost clears. It is worked
// out at a quarter of the screen's size and doubled up twice (it is a frost: the Show's processor takes 80 ms over it at half), from blurred
// copies of the page made once as the turn begins; it comes in over the page
// and goes back to it softly, so the page is never seen to change sharpness at once.

// entrySrc is the page as it was when the turn began, at half its size, at half its size blurred, and
// at a quarter blurred more: RGBA, each.
type entrySrc struct {
	w, h           int
	hw, hh, qw, qh int
	half, soft     []uint8
	quarter        []uint8
	halfW, softW   []uint32 // the same, a word to a pixel
	quarterW       []uint32
	ew, eh         int
	out            []uint32 // the entry worked out at a quarter size, lanes (double.go)
	outHalf        []uint32 // and doubled to half
	all, none      [][2]int32
	tables         entryTables
	// ready is how many of the blurred copies are made. They are made beside the frames, on the cores
	// the frames leave, from when the page is taken: no frame of the entry stops for them, and the
	// frost has barely begun by the time they are needed. made is closed when they all are.
	ready atomic.Int32
	made  chan struct{}
}

// captureEntry takes the page on dst for an entry, into old's memory if it is the right size.
func captureEntry(dst *image.RGBA, old *entrySrc) *entrySrc {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	e := old
	if e == nil || e.w != w || e.h != h {
		e = &entrySrc{w: w, h: h, hw: (w + 1) / 2, hh: (h + 1) / 2}
		e.qw, e.qh = (e.hw+1)/2, (e.hh+1)/2
		e.half, e.soft, e.outHalf = make([]uint8, e.hw*e.hh*4), make([]uint8, e.hw*e.hh*4), make([]uint32, e.hw*e.hh)
		e.ew, e.eh = (w+3)/4, (h+3)/4
		e.out = make([]uint32, e.ew*e.eh)
		// The doubling's runs: every cell of each half row, none of them left dark.
		e.all, e.none = make([][2]int32, e.hh), make([][2]int32, e.hh)
		for j := range e.hh {
			e.all[j], e.none[j] = [2]int32{0, int32(e.hw)}, [2]int32{int32(e.hw / 2), int32(e.hw / 2)}
		}
		e.quarter = make([]uint8, e.qw*e.qh*4)
		e.halfW, e.softW, e.quarterW = wordsOf(e.half), wordsOf(e.soft), wordsOf(e.quarter)
	}
	if e.made != nil {
		<-e.made // the last entry's copies, still being made into this memory
	}
	halve(wordsOf(dst.Pix), dst.Stride/4, w, h, e.halfW, e.hw, e.hh)
	e.ready.Store(0)
	e.made = make(chan struct{})
	go e.blur(e.made)
	return e
}

// blur makes the blurred copies, one after the other, and closes made: on one core, so the frames
// drawn meanwhile have the others.
func (e *entrySrc) blur(made chan struct{}) {
	defer close(made)
	copy(e.soft, e.half)
	boxBlurOn(1, e.soft, e.hw, e.hh, 2, 2)
	e.ready.Store(1)
	halve(e.halfW, e.hw, e.hw, e.hh, e.quarterW, e.qw, e.qh)
	boxBlurOn(1, e.quarter, e.qw, e.qh, 2, 3)
	e.ready.Store(2)
}

// halve averages each 2 by 2 block of src (w by h, a word to a pixel, stride words apart) into dst
// (dw by dh), all four bytes of a pixel at once.
func halve(src []uint32, stride, w, h int, dst []uint32, dw, dh int) {
	avg := func(a, b uint32) uint32 { return a&b + (a^b)&0xfefefefe>>1 }
	par(dh, func(lo, hi int) {
		for j := lo; j < hi; j++ {
			y0, y1 := min(2*j, h-1), min(2*j+1, h-1)
			a, b := src[y0*stride:y0*stride+w], src[y1*stride:y1*stride+w]
			out := dst[j*dw : (j+1)*dw]
			for i := range out {
				x0, x1 := min(2*i, w-1), min(2*i+1, w-1)
				out[i] = avg(avg(a[x0], a[x1]), avg(b[x0], b[x1])) | 0xff000000
			}
		}
	})
}

// boxBlur blurs img (w by h, RGBA) in place by passes of a box 2r+1 wide each way.
func boxBlur(img []uint8, w, h, r, passes int) { boxBlurOn(Workers, img, w, h, r, passes) }

// boxBlurOn is boxBlur on k goroutines.
func boxBlurOn(k int, img []uint8, w, h, r, passes int) {
	for range passes {
		blurRun(k, img, w, h, r, 4, w*4)
		blurCols(k, img, w, h, r)
	}
}

// blurCols is blurRun down img's columns, worked out a row at a time: a column read alone is a cache
// line a pixel, which on the Show's processor is most of the time the blur takes. The sums are the
// same, so the bytes are: the rows the box has passed are kept as they were, 2r+2 of them, since by
// then they are written over.
func blurCols(k int, img []uint8, w, h, r int) {
	stride := w * 4
	recip := 65536/uint32(2*r+1) + 1
	keep := 2*r + 2
	parOn(k, w, func(lo, hi int) {
		a, b := lo*4, hi*4
		m := b - a
		ring := make([]uint8, keep*m)
		kept := func(y int) []uint8 { return ring[y%keep*m : (y%keep+1)*m] }
		save := func(y int) { copy(kept(y), img[y*stride+a:y*stride+b]) }
		sum := make([]uint32, m)
		for y := 0; y <= min(r, h-1); y++ {
			save(y)
		}
		for k := -r; k <= r; k++ {
			src := kept(min(max(k, 0), h-1))
			for i, v := range src {
				sum[i] += uint32(v)
			}
		}
		for p := range h {
			out := img[p*stride+a : p*stride+b]
			for i := 0; i < m; i += 4 {
				out[i], out[i+1], out[i+2] = uint8(sum[i]*recip>>16), uint8(sum[i+1]*recip>>16), uint8(sum[i+2]*recip>>16)
			}
			in := p + r + 1
			if in < h {
				save(in)
			}
			add, sub := kept(min(in, h-1)), kept(max(p-r, 0))
			for i := range sum {
				sum[i] += uint32(add[i]) - uint32(sub[i])
			}
		}
	})
}

// blurRun averages runs of 2r+1 pixels along count lines n long, step bytes apart along a line and
// next apart between lines.
func blurRun(k int, img []uint8, n, count, r, step, next int) {
	parOn(k, count, func(lo, hi int) {
		line := make([]uint8, n*3)
		// A division is a call on the Show's processor: times the reciprocal instead, which rounds
		// to the same byte.
		recip := 65536/uint32(2*r+1) + 1
		for l := lo; l < hi; l++ {
			base := l * next
			for p := range n {
				o := base + p*step
				line[p*3], line[p*3+1], line[p*3+2] = img[o], img[o+1], img[o+2]
			}
			var s [3]uint32
			for k := -r; k <= r; k++ {
				p := min(max(k, 0), n-1) * 3
				s[0], s[1], s[2] = s[0]+uint32(line[p]), s[1]+uint32(line[p+1]), s[2]+uint32(line[p+2])
			}
			for p := range n {
				o := base + p*step
				img[o], img[o+1], img[o+2] = uint8(s[0]*recip>>16), uint8(s[1]*recip>>16), uint8(s[2]*recip>>16)
				out, in := max(p-r, 0)*3, min(p+r+1, n-1)*3
				for c := range 3 {
					s[c] += uint32(line[in+c]) - uint32(line[out+c])
				}
			}
		}
	})
}

// The range of distances from the wave's front, in design units, worked out each frame in steps of
// half a unit.
const (
	dxFrom = -800.0
	dxTo   = 1100.0
	dxN    = int((dxTo-dxFrom)*2) + 1
)

// entryTables is what one frame of the entry works out along the distance from the front, so that a
// pixel only looks it up: how far the page is pushed across (in design units) and how much of the
// push down goes with the ripple and with the bulge; how the frost mixes the page's copies (which two,
// and how much of the second, of 256); how much the frost brightens (bright, of 256) and the color
// with it (rich, of 256, with bright), how strong its tint is, and the pink band at the front.
type entryTables struct {
	push, rip, sink    [dxN]float32
	level              [dxN]uint8
	mix                [dxN]uint32
	bright, rich, tint [dxN]int32
	band               [dxN]int32
	frosted            [dxN]bool
}

// The entry's colors, 0 to 1.
var (
	tintWarm   = [3]float32{1, .59, .31}
	tintPink   = [3]float32{1, .24, .67}
	tintViolet = [3]float32{.59, .27, 1}
	bandPink   = [3]float32{1, .47, .82}
	bandPeach  = [3]float32{1, .75, .45}
	globeGold  = [3]float32{1, .66, .26}
	globePink  = [3]float32{1, .3, .62}
)

// tintTable is the frost's tint, 0 to 255, by the square of the distance from the right edge's middle
// as a share of 1.3 widths, 0 to 2 in 512 steps; globeTable the globe's color times its falloff by
// the square of the distance from its middle as a share of its size, 0 to 9.
var tintTable, globeTable = func() (t, g [512][3]int32) {
	for i := range 512 {
		rr := math.Sqrt(float64(i) / 511 * 2)
		a, b := float32(smooth(0, .35, rr)), float32(smooth(.35, 1, rr))
		for c := range 3 {
			t[i][c] = int32(lerp32(lerp32(tintWarm[c], tintPink[c], a), tintViolet[c], b)*255 + 0.5)
		}
		g2 := float64(i) / 511 * 9
		f, m := float32(math.Exp(-g2*1.6)), float32(smooth(0, 1, math.Sqrt(g2)))
		for c := range 3 {
			g[i][c] = int32(lerp32(globeGold[c], globePink[c], m)*f*255 + 0.5)
		}
	}
	return t, g
}()

// entry draws the entry a seconds in over dst, from the page as it was, and says how hard it pushes
// the light along the right edge.
func (s *Screen) entry(dst *image.RGBA, g *geometry, a float64) float64 {
	e := s.src
	pf := smooth(0.09, 0.74, a)
	front := g.wd + 110 - (g.wd+340)*pf
	bulge := 160 * (1 - 0.45*pf)
	haze := smooth(0.02, 0.3, a) * (1 - smooth(0.62, entryEnd, a))
	wave := smooth(0, 0.14, a) * (1 - smooth(0.55, 1.05, a))
	bell := math.Sin(math.Pi * clamp01((a-0.08)/(entryEnd-0.12)))
	scl := 1 - 0.035*bell*bell
	fg := smooth(0.03, 0.16, a) * (1 - smooth(0.5, 0.8, a))
	gR := 70 + 330*smooth(0, 0.42, a)
	gA := 0.8 * smooth(0, 0.1, a) * (1 - smooth(0.28, 0.62, a))
	// How much of the screen is the entry rather than the page: all of it, but for its first and last
	// moments, where the one goes over into the other.
	over := smooth(0, 0.12, a) * (1 - smooth(1.1, entryEnd, a))

	ready := int(e.ready.Load())
	tb := &e.tables
	for i := range dxN {
		dx := dxFrom + float64(i)/2
		env := float64(expNeg(float32(dx * dx / 3200)))
		trail := float64(expNeg(float32((dx - 110) * (dx - 110) / 5000)))
		var rip float64
		if env > 1e-4 {
			rip += env * math.Sin(dx*0.085-a*19)
		}
		if trail > 1e-4 {
			rip += 0.55 * trail * math.Sin(dx*0.06-a*14)
		}
		tb.push[i] = float32((-dx*env*0.28 + 7*rip) * wave)
		tb.rip[i], tb.sink[i] = float32(11*rip*wave), float32(-6*env*wave)
		h := haze * smooth(-90, 90, dx)
		// The frost's blur, rad design units wide: the half size copy going over into its blurred
		// one by 4, and that into the quarter's by 11.
		switch rad := 11 * h; {
		case rad < 0.35:
			tb.level[i], tb.mix[i] = 0, 0
		case rad < 4:
			tb.level[i], tb.mix[i] = 1, uint32(rad/4*256)
		default:
			tb.level[i], tb.mix[i] = 2, uint32(min((rad-4)/7, 1)*256)
		}
		// Short of a blurred copy not made yet, the most blurred there is.
		if int(tb.level[i]) > ready {
			tb.level[i], tb.mix[i] = uint8(ready), 256*uint32(min(ready, 1))
		}
		tb.frosted[i] = h > 0.002
		tb.bright[i] = int32((1 + 0.14*h) * 256)
		tb.rich[i] = int32((1 + 0.35*h) * (1 + 0.14*h) * 256)
		tb.tint[i] = int32(0.42 * h * 256)
		tb.band[i] = int32(float64(expNeg(float32(dx*dx/700))) * fg * 0.75 * 256)
	}

	k := float32(g.k)
	wd, hd := float32(g.wd), float32(g.hd)
	cx, cy := wd/2, hd/2
	inv := float32(1 / scl)
	rs := float32(cornerR)
	gA8 := int32(gA * 256)
	tintR := 1 / (wd * 1.3) / (wd * 1.3) * 511 / 2
	gr2 := 1 / float32(gR*gR) * 511 / 9
	hw, hh, ew, eh := e.hw, e.hh, e.ew, e.eh
	half, soft, quarter := e.halfW, e.softW, e.quarterW
	// A point's place in the half size copy, in 256ths of its pixels, from its place in design units.
	toHalf := k / 2 * 256
	maxX, maxY := int32(hw-1)*256, int32(hh-1)*256
	maxQX, maxQY := int32(e.qw-1)*256, int32(e.qh-1)*256
	par(eh, func(lo, hi int) {
		for j := lo; j < hi; j++ {
			py := (float32(4*j) + 2) / k
			qy := (py-cy)*inv + cy
			ny := (qy - hd*0.42) / (hd * 0.62)
			q := float64(qy)
			fx := float32(front - bulge*max(0, 1-float64(ny*ny)) + 12*math.Sin(q*0.022+a*8) + 6*math.Sin(q*0.051-a*11))
			sinRow := float32(math.Sin(q*0.018 + a*5))
			dyT := (qy - hd*0.42) * (qy - hd*0.42)
			gy := (py - hd*0.42) * (py - hd*0.42)
			bm := float32(smooth(-1, 1, float64(ny)))
			var bandC [3]int32
			for c := range 3 {
				bandC[c] = int32(lerp32(bandPink[c], bandPeach[c], bm) * 255)
			}
			ay := abs32(qy-cy) - (cy - rs)
			out := e.out[j*ew : (j+1)*ew]
			step := 4 / k * inv
			qx := ((2/k)-cx)*inv + cx
			for i := range ew {
				dx := qx - fx
				ti := min(max(int((dx-dxFrom)*2+0.5), 0), dxN-1)
				sx := qx + tb.push[ti]
				sy := qy + tb.rip[ti]*sinRow + tb.sink[ti]*ny
				X := min(max(int32(sx*toHalf)-128, 0), maxX)
				Y := min(max(int32(sy*toHalf)-128, 0), maxY)
				var c uint32
				switch tb.level[ti] {
				case 0:
					c = bilinear(half, hw, hh, X, Y)
				case 1:
					c = blend(bilinear(half, hw, hh, X, Y), bilinear(soft, hw, hh, X, Y), tb.mix[ti])
				default:
					// The quarter's pixels are twice the half's: smoothly between them too, or the
					// frost is in squares.
					QX, QY := min(max(X/2-64, 0), maxQX), min(max(Y/2-64, 0), maxQY)
					c = blend(bilinear(soft, hw, hh, X, Y), bilinear(quarter, e.qw, e.qh, QX, QY), tb.mix[ti])
				}
				band := tb.band[ti]
				ax := abs32(qx-cx) - (cx - rs)
				if !tb.frosted[ti] && band < 2 && gA8 == 0 && ax <= -1 && ay <= -1 {
					out[i] = c&0xff | (c&0xff00)<<2 | (c&0xff0000)<<4
					qx += step
					continue
				}
				r, gg, b := int32(c&0xff), int32(c>>8&0xff), int32(c>>16&0xff)
				if tb.frosted[ti] {
					// Richer and brighter, and tinted warm near the right edge to violet away from it.
					l := (77*r + 150*gg + 29*b) >> 8
					br, ri := l*tb.bright[ti], tb.rich[ti]
					r, gg, b = (br+(r-l)*ri)>>8, (br+(gg-l)*ri)>>8, (br+(b-l)*ri)>>8
					dxT := qx - wd
					tc := &tintTable[min(int((dxT*dxT+dyT)*tintR), 511)]
					f := tb.tint[ti]
					r, gg, b = r+tc[0]*f>>8, gg+tc[1]*f>>8, b+tc[2]*f>>8
				}
				if band >= 2 {
					r, gg, b = r+bandC[0]*band>>8, gg+bandC[1]*band>>8, b+bandC[2]*band>>8
				}
				if gA8 > 0 {
					gx := (float32(4*i)+2)/k - wd
					gx /= 0.6
					if gi := int((gx*gx + gy) * gr2); gi < 512 {
						gc := &globeTable[gi]
						r, gg, b = r+gc[0]*gA8>>8, gg+gc[1]*gA8>>8, b+gc[2]*gA8>>8
					}
				}
				// The page drawn back a little: beyond its rounded edge, rather than black, its edge carried
				// on out to the screen's, the most blurred copy there is of it and a little darker, gone
				// into softly, so the page shrinks without a band of black round it.
				if ax > -1 || ay > -1 {
					sd := sdRound(ax, ay, rs) * float32(scl)
					if out := int32(smooth(-3, 9, float64(sd)) * 256); out > 0 {
						var bc uint32
						switch {
						case ready >= 2:
							bc = bilinear(quarter, e.qw, e.qh, min(max(X/2-64, 0), maxQX), min(max(Y/2-64, 0), maxQY))
						case ready >= 1:
							bc = bilinear(soft, hw, hh, X, Y)
						default:
							bc = c
						}
						br, bg, bb := int32(bc&0xff), int32(bc>>8&0xff), int32(bc>>16&0xff)
						r, gg, b = r+(br-r)*out>>8, gg+(bg-gg)*out>>8, b+(bb-b)*out>>8
						dim := 256 - out*3/10
						r, gg, b = r*dim>>8, gg*dim>>8, b*dim>>8
					}
				}
				out[i] = uint32(min(max(r, 0), 255)) | uint32(min(max(gg, 0), 255))<<10 | uint32(min(max(b, 0), 255))<<20
				qx += step
			}
		}
	})
	if over > 0 {
		up2(e.out, ew, eh, e.outHalf, hw, hh, e.none, e.all)
		putUp2(dst, e.outHalf, hw, hh, uint32(over*256+0.5))
	}
	return push(a)
}

// push is how far the entry pushes the light along the right at a, 0 to 1.
func push(a float64) float64 { return smooth(0.14, 0.42, a) * (1 - smooth(0.62, 1.35, a)) }

// bilinear is img (w by h, a word to a pixel) at X, Y in 256ths of its pixels, smoothly between them.
func bilinear(img []uint32, w, h int, X, Y int32) uint32 {
	i, j := int(X>>8), int(Y>>8)
	fx, fy := uint32(X&255), uint32(Y&255)
	o := j*w + i
	di, dj := 0, 0
	if i+1 < w {
		di = 1
	}
	if j+1 < h {
		dj = w
	}
	top := blend(img[o], img[o+di], fx)
	bot := blend(img[o+dj], img[o+dj+di], fx)
	return blend(top, bot, fy)
}

// sdRound is how far outside a rounded rectangle a point is, given how far beyond the rectangle's
// inner corners it is each way (ax, ay) and the rounding.
func sdRound(ax, ay, r float32) float32 {
	qx, qy := maxf(ax, 0), maxf(ay, 0)
	return float32(math.Sqrt(float64(qx*qx+qy*qy))) + minf(maxf(ax, ay), 0) - r
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func clamp8(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}
