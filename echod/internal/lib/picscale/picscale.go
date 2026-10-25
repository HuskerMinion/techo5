// Package picscale fits a picture to a size: a photo, a camera frame or a dashboard picture scaled to
// the panel.
//
// x/image/draw's ApproxBiLinear did this, at about 105 ms for a 1280×800 frame on the 2nd gen Show 8
// and 120 ms for 960×480 on the 2nd gen Show 5, whatever the source: for every pixel it works out four
// source positions in floating point, turns four YCbCr samples into color, and blends them. Here the
// filter is separable and worked out once per row and column rather than per pixel, in integers; a
// YCbCr picture is scaled plane by plane and turned into color once a pixel, at the size it is drawn;
// on arm64 the two passes are NEON (scale_arm64.s); and the rows are shared between the cores.
//
// The filter is CatmullRom, as xdraw.CatmullRom is, widened when shrinking so that every source
// pixel counts: sharper than bilinear, and without the aliasing ApproxBiLinear has once a picture is
// shrunk by more than a little.
package picscale

import (
	"image"
	"image/draw"
	"math"
	"runtime"
	"sync"
	"sync/atomic"

	xdraw "golang.org/x/image/draw"
)

// Fixed point. A vertical pass's weights sum to 1<<vBits and a horizontal pass's to 1<<hBits, and
// between the two a sample keeps midBits of fraction in an int16. The horizontal pass ends with a
// shift of hBits+midBits = 16, the most the NEON narrowing shift takes.
const (
	vBits   = 14
	hBits   = 12
	midBits = 4
	vShift  = vBits - midBits
	hShift  = hBits + midBits
)

// wide is the most taps the NEON horizontal passes take an output: a filter with fewer is padded with
// zero weights to four or to wide, and one with more is left to the Go code.
const wide = 8

// Kernel is a filter to scale with: weight(x) for a sample x away, in the output's spacing (the
// source's, when enlarging), out to radius. When shrinking it is widened, so every source pixel counts.
type Kernel struct {
	radius   float64
	weight   func(x float64) float64
	fallback xdraw.Scaler
}

var (
	// BiLinear is a tent: xdraw.BiLinear, which it falls back on.
	BiLinear = &Kernel{1, func(x float64) float64 { return 1 - x }, xdraw.BiLinear}
	// CatmullRom is the sharp cubic xdraw.CatmullRom is. It is what pictures are fitted to the panel
	// with, falling back on xdraw.ApproxBiLinear, which it replaced.
	CatmullRom = &Kernel{2, func(x float64) float64 {
		if x < 1 {
			return (3*x-5)*x*x/2 + 1
		}
		return ((5-x)*x-8)*x/2 + 2
	}, xdraw.ApproxBiLinear}
)

// Scale draws src's sr scaled to dr in dst, with op as xdraw does. A picture it does not take (see
// scale) is drawn by the kernel's x/image/draw fallback instead.
func (k *Kernel) Scale(dst *image.RGBA, dr image.Rectangle, src image.Image, sr image.Rectangle, op draw.Op) {
	// The 32-bit boards (the 1st gen Shows, the Show 5 on Amazon's kernel, the Spot) keep the fallback
	// they had: the Go passes are unmeasured there, and the camera view scales every frame.
	if runtime.GOARCH == "arm" || !k.scale(dst, dr, src, sr, op) {
		k.fallback.Scale(dst, dr, src, sr, op, nil)
	}
}

// scale draws src's sr scaled to dr in dst, as the same filter in x/image/draw does, and reports
// whether it did. It takes the pictures this daemon scales: YCbCr at any subsampling, Gray and RGBA,
// with Over only for one that is opaque (where it is the same as Src). Anything else is left to the
// caller, untouched.
func (k *Kernel) scale(dst *image.RGBA, dr image.Rectangle, src image.Image, sr image.Rectangle, op draw.Op) bool {
	if op != draw.Src && op != draw.Over {
		return false
	}
	var p *picture
	switch m := src.(type) {
	case *image.YCbCr:
		if !sr.In(m.Rect) {
			return false
		}
		fx, fy, ok := subsampling(m.SubsampleRatio)
		if !ok {
			return false
		}
		// image.YCbCr's own COffset rounds the corner down the same way.
		cx, cy := m.Rect.Min.X/fx, m.Rect.Min.Y/fy
		p = &picture{kind: kindYCbCr, planes: [3]plane{
			{m.Y, m.YStride, m.Rect.Min.X, m.Rect.Min.Y, 1, 1},
			{m.Cb, m.CStride, cx, cy, fx, fy},
			{m.Cr, m.CStride, cx, cy, fx, fy},
		}}
	case *image.Gray:
		if !sr.In(m.Rect) {
			return false
		}
		p = &picture{kind: kindGray, planes: [3]plane{{m.Pix, m.Stride, m.Rect.Min.X, m.Rect.Min.Y, 1, 1}}}
	case *image.RGBA:
		if !sr.In(m.Rect) || (op == draw.Over && !m.SubImage(sr).(*image.RGBA).Opaque()) {
			return false
		}
		p = &picture{kind: kindRGBA, planes: [3]plane{{m.Pix, m.Stride, m.Rect.Min.X, m.Rect.Min.Y, 1, 1}}}
	default:
		return false
	}
	if sr.Min.X < 0 || sr.Min.Y < 0 {
		return false // the planes' corners are worked out for pictures at or right of and below zero
	}
	clip := dr.Intersect(dst.Rect)
	if sr.Empty() || clip.Empty() {
		return true
	}
	p.scale(k, dst, dr, clip, sr)
	return true
}

// subsampling is how many luma samples a chroma sample spans, across and down.
func subsampling(r image.YCbCrSubsampleRatio) (fx, fy int, ok bool) {
	switch r {
	case image.YCbCrSubsampleRatio444:
		return 1, 1, true
	case image.YCbCrSubsampleRatio422:
		return 2, 1, true
	case image.YCbCrSubsampleRatio420:
		return 2, 2, true
	case image.YCbCrSubsampleRatio440:
		return 1, 2, true
	case image.YCbCrSubsampleRatio411:
		return 4, 1, true
	case image.YCbCrSubsampleRatio410:
		return 4, 2, true
	}
	return 0, 0, false
}

const (
	kindYCbCr = iota
	kindGray
	kindRGBA
)

// plane is one array of samples: pix[(y-y0)*stride + (x-x0)*channels] for the sample at (x, y) in the
// plane's own coordinates, which are the picture's divided by fx and fy.
type plane struct {
	pix    []byte
	stride int
	x0, y0 int
	fx, fy int
}

type picture struct {
	kind   int
	planes [3]plane
}

// taps is a filter along one axis: output i takes n samples from lo+start[i] on, weighted by
// w[i*n:(i+1)*n]. Every output has the same n, short ones padded with zero weights on samples that
// exist, so the loops need no per-output count.
type taps struct {
	start []int32
	w     []int16
	n     int
	// unit is a filter that copies: output i is sample lo+i.
	unit bool
	// lo and hi are the samples any output reads, in the plane's own coordinates.
	lo, hi int
}

// axis works out the filter along one axis: outputs o0 to o1 (of a target dlen long, from d0) from
// the source's s0..s0+slen, in a plane whose samples each span f of the picture's. The weights sum to
// 1<<bits. With pad, a filter of fewer than wide taps is padded to wide, or to four where it has no
// more and pad4, for the NEON passes; the samples a padded output reads past hi have no weight.
func (kern *Kernel) axis(d0, dlen, o0, o1, s0, slen, f, bits int, pad, pad4 bool) taps {
	ratio := float64(slen) / float64(dlen) // source pixels an output pixel spans
	support := max(ratio/float64(f), 1)    // in the plane's samples
	reach := kern.radius * support
	// The samples that exist inside the source rectangle, in the plane's own coordinates.
	first, last := floorDiv(s0, f), floorDiv(s0+slen-1, f)
	// The most samples whose centers can fall within reach of a point, but never more than there are.
	n := min(int(math.Ceil(2*reach)), last-first+1)
	outs := o1 - o0
	start := make([]int, outs)
	w := make([]int32, outs*n)
	fw := make([]float64, n)
	span := 1 // the most samples, first to last, with a weight
	for o := range outs {
		// The output's center, in the plane's coordinates, where sample k is centered at k+0.5.
		c := (float64(s0) + (float64(o0+o-d0)+0.5)*ratio) / float64(f)
		// The first sample whose center is within reach of c, and the window moved inward where it
		// would run off the source.
		a := int(math.Floor(c-reach-0.5)) + 1
		a = max(min(a, last+1-n), first)
		var sum float64
		for k := range n {
			fw[k] = 0
			if x := math.Abs(float64(a+k)+0.5-c) / support; x < kern.radius {
				fw[k] = kern.weight(x)
			}
			sum += fw[k]
		}
		if sum == 0 {
			// Beyond the edge: the nearest sample.
			k := min(max(int(math.Floor(c))-a, 0), n-1)
			fw[k], sum = 1, 1
		}
		row := w[o*n : (o+1)*n]
		total, big := int32(0), 0
		for k := range n {
			row[k] = int32(math.Round(fw[k] / sum * float64(int(1)<<bits)))
			total += row[k]
			if row[k] > row[big] {
				big = k
			}
		}
		row[big] += 1<<bits - total // exactly one, so a flat picture stays flat
		start[o] = a
		lo, hi := nonzero(row)
		span = max(span, hi-lo)
	}

	// Trimmed to the taps any output weights: a picture drawn at its own size takes one of
	// CatmullRom's four, and a little shrinking leaves the outermost ones too small to count.
	m := min(span, n)
	// A filter that copies, output o being one sample on from output o-1 at full weight.
	unit := m == 1
	at0 := start[0] + nz(w[:n])
	for o := 0; unit && o < outs; o++ {
		k := nz(w[o*n : (o+1)*n])
		unit = start[o]+k == at0+o && w[o*n+k] == 1<<bits
	}
	stride := m
	switch {
	case unit || !pad:
	case pad4 && m <= 4:
		stride = 4
	case m <= wide:
		stride = wide
	}
	t := taps{start: make([]int32, outs), w: make([]int16, outs*stride), n: stride, unit: unit, lo: last + 1, hi: first}
	for o := range outs {
		row := w[o*n : (o+1)*n]
		lo, hi := nonzero(row)
		a := min(start[o]+lo, last+1-m)
		copy16(t.w[o*stride+start[o]+lo-a:], row[lo:hi])
		start[o] = a
		t.lo, t.hi = min(t.lo, a), max(t.hi, a+m)
	}
	for o := range outs {
		t.start[o] = int32(start[o] - t.lo)
	}
	return t
}

// nz is the first weight of row that isn't zero.
func nz(row []int32) int {
	lo, _ := nonzero(row)
	return lo
}

// nonzero is the span of row from its first weight that isn't zero to just past its last.
func nonzero(row []int32) (lo, hi int) {
	for lo < len(row)-1 && row[lo] == 0 {
		lo++
	}
	hi = len(row)
	for hi > lo+1 && row[hi-1] == 0 {
		hi--
	}
	return lo, hi
}

func copy16(dst []int16, src []int32) {
	for i, v := range src {
		dst[i] = int16(v)
	}
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// planeTaps are one plane's filters, both ways.
type planeTaps struct{ x, y taps }

// chunk is how many rows a core takes at a time: few enough that a core that is slower, or busy
// with something else, holds the rest up for little.
const chunk = 16

func (p *picture) scale(k *Kernel, dst *image.RGBA, dr, clip, sr image.Rectangle) {
	nplanes := 1
	chans := 1
	switch p.kind {
	case kindYCbCr:
		nplanes = 2 // Cr is laid out as Cb is, and shares its filters
	case kindRGBA:
		chans = 4
	}
	var pt [2]planeTaps
	for i := range nplanes {
		pl := &p.planes[i]
		pt[i] = planeTaps{
			x: k.axis(dr.Min.X, dr.Dx(), clip.Min.X, clip.Max.X, sr.Min.X, sr.Dx(), pl.fx, hBits, haveNEON, chans == 1),
			y: k.axis(dr.Min.Y, dr.Dy(), clip.Min.Y, clip.Max.Y, sr.Min.Y, sr.Dy(), pl.fy, vBits, false, false),
		}
	}

	rows := clip.Dy()
	chunks := (rows + chunk - 1) / chunk
	workers := min(runtime.GOMAXPROCS(0), 4, chunks)
	var next atomic.Int32
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			r := newRower(p, clip.Dx(), chans, &pt)
			for {
				c := int(next.Add(1)) - 1
				if c >= chunks {
					return
				}
				for y := clip.Min.Y + c*chunk; y < min(clip.Min.Y+(c+1)*chunk, clip.Max.Y); y++ {
					r.row(dst.Pix[dst.PixOffset(clip.Min.X, y):][:clip.Dx()*4], y-clip.Min.Y)
				}
			}
		})
	}
	wg.Wait()
}

// rower draws one row at a time, with buffers of its own.
type rower struct {
	p     *picture
	pt    *[2]planeTaps
	chans int
	mid   [3][]int16
	out   [3][]byte
	acc   []int32
}

func newRower(p *picture, w, chans int, pt *[2]planeTaps) *rower {
	r := &rower{p: p, pt: pt, chans: chans}
	nplanes := 1
	if p.kind == kindYCbCr {
		nplanes = 3
	}
	widest := 0
	for i := range nplanes {
		t := &pt[min(i, 1)].x
		// Room past hi for a padded filter's last outputs to read, at no weight.
		r.mid[i] = make([]int16, (t.hi-t.lo+wide)*chans)
		r.out[i] = make([]byte, w*chans)
		widest = max(widest, (t.hi-t.lo)*chans)
	}
	r.acc = make([]int32, widest)
	return r
}

// row draws output row oy of the clip into row.
func (r *rower) row(row []byte, oy int) {
	nplanes := 1
	if r.p.kind == kindYCbCr {
		nplanes = 3
	}
	var out [3][]byte
	for i := range nplanes {
		pl, t := &r.p.planes[i], &r.pt[min(i, 1)]
		ty := &t.y
		off := (ty.lo+int(ty.start[oy])-pl.y0)*pl.stride + (t.x.lo-pl.x0)*r.chans
		out[i] = r.out[i]
		switch mid := r.mid[i][:(t.x.hi-t.x.lo)*r.chans]; {
		case t.x.unit && ty.unit:
			out[i] = pl.pix[off:][:len(r.out[i])] // at its own size: the samples themselves
		case t.x.unit:
			vertical(mid, pl.pix[off:], pl.stride, ty.w[oy*ty.n:(oy+1)*ty.n], r.acc)
			narrow(out[i], mid)
		default:
			vertical(mid, pl.pix[off:], pl.stride, ty.w[oy*ty.n:(oy+1)*ty.n], r.acc)
			if r.chans == 4 {
				horizontal4(out[i], r.mid[i], &t.x)
			} else {
				horizontal(out[i], r.mid[i], &t.x)
			}
		}
	}
	switch r.p.kind {
	case kindYCbCr:
		toRGBA(row, out[0], out[1], out[2])
	case kindGray:
		grayToRGBA(row, out[0])
	case kindRGBA:
		copy(row, out[0])
	}
}

// vertical sets mid[x] to rows of src (stride apart, the first at src[0]) weighted by w, with midBits
// of fraction. acc is scratch at least as long as mid.
func vertical(mid []int16, src []byte, stride int, w []int16, acc []int32) {
	if n := len(mid) / 16 * 16; haveNEON && n > 0 {
		verticalNEON(&mid[0], &src[0], stride, &w[0], len(w), n/16)
		mid, src = mid[n:], src[n:]
		if len(mid) == 0 {
			return
		}
	}
	verticalGo(mid, src, stride, w, acc)
}

func verticalGo(mid []int16, src []byte, stride int, w []int16, acc []int32) {
	const half = 1 << (vShift - 1)
	n := len(mid)
	switch len(w) {
	case 1:
		r0 := src[:n]
		for x := range mid {
			mid[x] = int16((int32(r0[x])*int32(w[0]) + half) >> vShift)
		}
	case 2:
		r0, r1 := src[:n], src[stride:][:n]
		w0, w1 := int32(w[0]), int32(w[1])
		for x := range mid {
			mid[x] = int16((int32(r0[x])*w0 + int32(r1[x])*w1 + half) >> vShift)
		}
	case 3:
		r0, r1, r2 := src[:n], src[stride:][:n], src[2*stride:][:n]
		w0, w1, w2 := int32(w[0]), int32(w[1]), int32(w[2])
		for x := range mid {
			mid[x] = int16((int32(r0[x])*w0 + int32(r1[x])*w1 + int32(r2[x])*w2 + half) >> vShift)
		}
	case 4:
		r0, r1, r2, r3 := src[:n], src[stride:][:n], src[2*stride:][:n], src[3*stride:][:n]
		w0, w1, w2, w3 := int32(w[0]), int32(w[1]), int32(w[2]), int32(w[3])
		for x := range mid {
			mid[x] = int16((int32(r0[x])*w0 + int32(r1[x])*w1 + int32(r2[x])*w2 + int32(r3[x])*w3 + half) >> vShift)
		}
	default:
		acc = acc[:n]
		clear(acc)
		for k, wk := range w {
			r, w32 := src[k*stride:][:n], int32(wk)
			for x := range acc {
				acc[x] += int32(r[x]) * w32
			}
		}
		for x := range mid {
			mid[x] = int16((acc[x] + half) >> vShift)
		}
	}
}

// narrow is a horizontal pass that copies: mid rounded back to samples.
func narrow(out []byte, mid []int16) {
	const half = 1 << (midBits - 1)
	mid = mid[:len(out)]
	for i, v := range mid {
		out[i] = clamp((int32(v) + half) >> midBits)
	}
}

// horizontal sets out[i] to mid filtered by t, one channel.
func horizontal(out []byte, mid []int16, t *taps) {
	if n := len(out) / 8 * 8; haveNEON && (t.n == 4 || t.n == wide) && n > 0 {
		if t.n == 4 {
			horizontal4tapsNEON(&out[0], &mid[0], &t.start[0], &t.w[0], n/8)
		} else {
			horizontalNEON(&out[0], &mid[0], &t.start[0], &t.w[0], n/8)
		}
		horizontalGo(out[n:], mid, t.start[n:], t.w[n*t.n:], t.n)
		return
	}
	horizontalGo(out, mid, t.start, t.w, t.n)
}

func horizontalGo(out []byte, mid []int16, start []int32, wt []int16, n int) {
	const half = 1 << (hShift - 1)
	start, wt = start[:len(out)], wt[:len(out)*n]
	switch n {
	case 1:
		for i := range out {
			out[i] = clamp((int32(mid[start[i]])*int32(wt[i]) + half) >> hShift)
		}
	case 2:
		for i := range out {
			m := (*[2]int16)(mid[start[i]:])
			w := (*[2]int16)(wt[2*i:])
			out[i] = clamp((int32(m[0])*int32(w[0]) + int32(m[1])*int32(w[1]) + half) >> hShift)
		}
	case 3:
		for i := range out {
			m := (*[3]int16)(mid[start[i]:])
			w := (*[3]int16)(wt[3*i:])
			out[i] = clamp((int32(m[0])*int32(w[0]) + int32(m[1])*int32(w[1]) + int32(m[2])*int32(w[2]) + half) >> hShift)
		}
	case 4:
		for i := range out {
			m := (*[4]int16)(mid[start[i]:])
			w := (*[4]int16)(wt[4*i:])
			out[i] = clamp((int32(m[0])*int32(w[0]) + int32(m[1])*int32(w[1]) + int32(m[2])*int32(w[2]) +
				int32(m[3])*int32(w[3]) + half) >> hShift)
		}
	default:
		for i := range out {
			m := mid[start[i]:][:n]
			w := wt[i*n:][:n]
			var v int32
			for k := range m {
				v += int32(m[k]) * int32(w[k])
			}
			out[i] = clamp((v + half) >> hShift)
		}
	}
}

// horizontal4 is horizontal for four channels a sample, interleaved.
func horizontal4(out []byte, mid []int16, t *taps) {
	if n := len(out) / 8 * 8; haveNEON && t.n == wide && n > 0 {
		horizontal4NEON(&out[0], &mid[0], &t.start[0], &t.w[0], n/8)
		horizontal4Go(out[n:], mid, t.start[n/4:], t.w[n/4*wide:], t.n)
		return
	}
	horizontal4Go(out, mid, t.start, t.w, t.n)
}

func horizontal4Go(out []byte, mid []int16, start []int32, wt []int16, n int) {
	const half = 1 << (hShift - 1)
	for i := range len(out) / 4 {
		s := int(start[i]) * 4
		m := mid[s : s+4*n]
		w := wt[i*n : i*n+n]
		var r, g, b, a int32
		for k, wk := range w {
			w32 := int32(wk)
			r += int32(m[4*k]) * w32
			g += int32(m[4*k+1]) * w32
			b += int32(m[4*k+2]) * w32
			a += int32(m[4*k+3]) * w32
		}
		o := (*[4]byte)(out[4*i:])
		o[0], o[1], o[2], o[3] = clamp((r+half)>>hShift), clamp((g+half)>>hShift), clamp((b+half)>>hShift), clamp((a+half)>>hShift)
	}
}

func clamp(v int32) byte {
	if uint32(v) > 255 {
		if v < 0 {
			return 0
		}
		return 255
	}
	return byte(v)
}

// toRGBA turns a row of Y, Cb and Cr into opaque RGBA, as color.YCbCrToRGB does.
func toRGBA(row, y, cb, cr []byte) {
	if n := len(y) / 8 * 8; haveNEON && n > 0 {
		toRGBANEON(&row[0], &y[0], &cb[0], &cr[0], n/8)
		row, y, cb, cr = row[4*n:], y[n:], cb[n:], cr[n:]
	}
	toRGBAGo(row, y, cb, cr)
}

func toRGBAGo(row, y, cb, cr []byte) {
	n := len(y)
	row = row[:4*n]
	cb, cr = cb[:n], cr[:n]
	for i := range y {
		yy1 := int32(y[i]) * 0x10101
		c1 := int32(cb[i]) - 128
		c2 := int32(cr[i]) - 128
		o := (*[4]byte)(row[4*i:])
		o[0] = clamp16(yy1 + 91881*c2)
		o[1] = clamp16(yy1 - 22554*c1 - 46802*c2)
		o[2] = clamp16(yy1 + 116130*c1)
		o[3] = 255
	}
}

// clamp16 is color.YCbCrToRGB's rounding: v is the color with 16 bits of fraction.
func clamp16(v int32) byte {
	if uint32(v)&0xff000000 == 0 {
		return byte(v >> 16)
	}
	return ^byte(v >> 31)
}

func grayToRGBA(row, y []byte) {
	row = row[:4*len(y)]
	for i, v := range y {
		o := (*[4]byte)(row[4*i:])
		o[0], o[1], o[2], o[3] = v, v, v, 255
	}
}
