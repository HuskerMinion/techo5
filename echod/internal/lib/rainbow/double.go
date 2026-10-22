package rainbow

import (
	"image"
	"unsafe"
)

// Doubling a picture's size: across, each pixel of the larger is three quarters of the nearest pixel
// of the smaller and a quarter of the next; down, each row of the smaller is two of the larger. These
// are the loops a frame spends most of its time in, the glow's doubled over a third of the screen and
// the entry's over all of it, and the Show's processor is 32-bit, where Go reads a byte at a time what
// it is given as bytes. So a picture being doubled is kept one word to a pixel, its three colors ten
// bits apart (lanes), and three colors are mixed in one addition: three of a lane plus one, plus two to
// round, is at most 1022 and stays in its ten bits. The screen is read and written a word to a pixel
// too, and the glow added to it a word at a time, each byte stopping at white.

// A pixel as lanes: red in the low ten bits, green in the next, blue above. laneMask keeps the eight
// bits of each lane that hold a color, and laneRound is two in each, to round a sum of four.
const (
	laneMask  = 0xff | 0xff<<10 | 0xff<<20
	laneRound = 2 | 2<<10 | 2<<20
)

func lanes(r, g, b uint8) uint32 { return uint32(r) | uint32(g)<<10 | uint32(b)<<20 }

// mix4 is a quarter of p and three quarters of c, in lanes.
func mix4(p, c uint32) uint32 { return (p + 3*c + laneRound) >> 2 & laneMask }

// rgb is lanes as the screen holds a pixel, red in the low byte, with no alpha; pixel the same,
// opaque.
func rgb(l uint32) uint32   { return l&0xff | l>>2&0xff00 | l>>4&0xff0000 }
func pixel(l uint32) uint32 { return rgb(l) | 0xff000000 }

// words is img's pixels a word each, as they lie in its memory: little-endian, red first.
func words(img *image.RGBA) []uint32 { return wordsOf(img.Pix) }

func wordsOf(pix []uint8) []uint32 {
	if len(pix) == 0 {
		return nil
	}
	return unsafe.Slice((*uint32)(unsafe.Pointer(&pix[0])), len(pix)/4)
}

// addSat adds the bytes of a and b, each stopping at 255.
func addSat(a, b uint32) uint32 {
	s := a&0x7f7f7f7f + b&0x7f7f7f7f
	carry := (a&b | (a|b)&s) & 0x80808080
	return s ^ (a^b)&0x80808080 | (carry>>7)*0xff
}

// blend is f of 256 of b over a, each byte.
func blend(a, b, f uint32) uint32 {
	g := 256 - f
	rb := (a&0x00ff00ff*g + b&0x00ff00ff*f) >> 8 & 0x00ff00ff
	ga := (a>>8&0x00ff00ff*g + b>>8&0x00ff00ff*f) & 0xff00ff00
	return rb | ga
}

// pair is the two cells of a grid n long that cell i of one twice the size is drawn from: the
// quarter's first, the three quarters' second.
func pair(i, n int) (int, int) {
	m := min(i/2, n-1)
	if i%2 == 0 {
		return max(m-1, 0), m
	}
	return min(m+1, n-1), m
}

// spread doubles cells from i0 to i1 of src (n long, lanes) across into dst, as the screen holds
// pixels, with no alpha: pixels 2*i0 to 2*i1, as far as dst goes.
func spread(dst, src []uint32, i0, i1 int) {
	n := len(src)
	if i0 >= i1 {
		return
	}
	p := src[max(i0-1, 0)]
	c := src[i0]
	for i := i0; i < i1; i++ {
		q := c
		if i+1 < n {
			q = src[i+1]
		}
		if x := 2 * i; x+1 < len(dst) {
			dst[x], dst[x+1] = rgb(mix4(p, c)), rgb(mix4(q, c))
		} else if x < len(dst) {
			dst[x] = rgb(mix4(p, c))
		}
		p, c = c, q
	}
}

// addAll adds g to out, a pixel at a time, each byte stopping at 255.
func addAll(out, g []uint32) {
	g = g[:len(out)]
	for x, v := range out {
		out[x] = addSat(v, g[x])
	}
}

// up2 doubles src (sw by sh, lanes) into dst (dw by dh, lanes) smoothly both ways, within each of
// dst's rows' run of cells reach, leaving out its inner run of cells, which stays dark.
func up2(src []uint32, sw, sh int, dst []uint32, dw, dh int, inner, reach [][2]int32) {
	par(dh, func(lo, hi int) {
		row := make([]uint32, sw)
		for j := lo; j < hi; j++ {
			r0, r1 := pair(j, sh)
			a, b := src[r0*sw:(r0+1)*sw], src[r1*sw:(r1+1)*sw]
			var lit uint32
			for i := range row {
				v := mix4(a[i], b[i])
				row[i] = v
				lit |= v
			}
			out := dst[j*dw : (j+1)*dw]
			from, to := int(inner[j][0]), int(inner[j][1])
			first, last := int(reach[j][0]), int(reach[j][1])
			if first >= last {
				continue
			}
			if lit == 0 {
				clear(out[first:max(first, min(from, last))])
				clear(out[min(max(to, first), last):last])
				continue
			}
			// In lanes rather than as pixels: spread's halves, without the packing.
			put := func(i0, i1 int) {
				if i0 >= i1 {
					return
				}
				p, c := row[max(i0-1, 0)], row[i0]
				for i := i0; i < i1; i++ {
					q := c
					if i+1 < sw {
						q = row[i+1]
					}
					if x := 2 * i; x+1 < dw {
						out[x], out[x+1] = mix4(p, c), mix4(q, c)
					} else if x < dw {
						out[x] = mix4(p, c)
					}
					p, c = c, q
				}
			}
			if from < to {
				put(first/2, (from+1)/2)
				put(to/2, (last+1)/2)
			} else {
				put(first/2, (last+1)/2)
			}
		}
	})
}

// dim is the bits of a cell in lanes: a cell with none of them set adds nothing and is left out.
// (Leaving out the faintest as well draws their edge on a dark page.)
const dim = laneMask

// addUp2 adds src (sw by sh, lanes) onto dst's pixels, each cell doubled each way: smoothly across
// and down near the edge (outside each half row's reach run), where the line along the edge is sharp, and
// cell for cell beyond it, where the glow is soft and four pixels the same are not to be told from four
// that are not. The half rows' inner runs, and dark cells, are left out.
func addUp2(dst *image.RGBA, src []uint32, sw, sh int, inner, reach [][2]int32) {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	pix, stride := words(dst), dst.Stride/4
	par(sh, func(lo, hi int) {
		row, row1 := make([]uint32, w), make([]uint32, w)
		va, vb := make([]uint32, sw), make([]uint32, sw)
		for j := lo; j < hi; j++ {
			cells := src[j*sw : (j+1)*sw]
			// The rows above and below, which the two rows of pixels take a quarter of each, as up2's do:
			// near the edge the line is sharp, and a row of cells drawn twice is a step at the corners.
			above, below := src[max(j-1, 0)*sw:max(j-1, 0)*sw+sw], src[min(j+1, sh-1)*sw:min(j+1, sh-1)*sw+sw]
			from, to := int(inner[j][0]), int(inner[j][1])
			first, last := int(reach[j][0]), int(reach[j][1])
			y0, y1 := 2*j, 2*j+1
			r0 := pix[y0*stride : y0*stride+w]
			var r1 []uint32
			if y1 < h {
				r1 = pix[y1*stride : y1*stride+w]
			}
			// Near the edge: the cells before the reach run and after it, all of a row near the top
			// or the bottom.
			near := [2][2]int{{0, sw}, {sw, sw}}
			if first < last {
				near = [2][2]int{{0, first}, {last, sw}}
			}
			for _, sp := range near {
				a, b := sp[0], sp[1]
				for a < b && (cells[a]|above[a]|below[a])&dim == 0 {
					a++
				}
				for b > a && (cells[b-1]|above[b-1]|below[b-1])&dim == 0 {
					b--
				}
				if a >= b {
					continue
				}
				// The pixels of the dark cell next to a lit one take some of it, within the span: the
				// cells beyond it are added on their own.
				a, b = max(a-1, sp[0]), min(b+1, sp[1])
				for i := max(a-1, 0); i < min(b+1, sw); i++ {
					va[i], vb[i] = mix4(above[i], cells[i]), mix4(below[i], cells[i])
				}
				x0, x1 := 2*a, min(2*b, w)
				spread(row, va, a, b)
				addAll(r0[x0:x1], row[x0:x1])
				if r1 != nil {
					spread(row1, vb, a, b)
					addAll(r1[x0:x1], row1[x0:x1])
				}
			}
			if first >= last {
				continue
			}
			// Beyond it, round the dark middle.
			if from < to {
				addCells(r0, r1, cells, first, min(from, last))
				addCells(r0, r1, cells, max(to, first), last)
			} else {
				addCells(r0, r1, cells, first, last)
			}
		}
	})
}

// addCells adds cells a to b of a row of cells (lanes) to the pixels of the two rows of the screen it
// covers, each cell on two pixels of each; r1 is nil under the last row of an odd screen.
func addCells(r0, r1, cells []uint32, a, b int) {
	w := len(r0)
	for i := a; i < b; i++ {
		c := cells[i]
		if c&dim == 0 {
			continue
		}
		v, x := rgb(c), 2*i
		if x+1 >= w {
			if x < w {
				r0[x] = addSat(r0[x], v)
				if r1 != nil {
					r1[x] = addSat(r1[x], v)
				}
			}
			continue
		}
		r0[x], r0[x+1] = addSat(r0[x], v), addSat(r0[x+1], v)
		if r1 != nil {
			r1[x], r1[x+1] = addSat(r1[x], v), addSat(r1[x+1], v)
		}
	}
}

// putUp2 doubles src (sw by sh, lanes) onto dst, over of 256 of it over what is there: smoothly both
// ways, each row of pixels a quarter of the row of cells above it or below, as up2's (a row of cells
// drawn twice is a band of two pixels, and the entry is all of the screen). In one pass: each cell
// mixed down as it is reached, and its two pixels written from it and its neighbors.
func putUp2(dst *image.RGBA, src []uint32, sw, sh int, over uint32) {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	if over == 0 {
		return
	}
	pix, stride := words(dst), dst.Stride/4
	par(sh, func(lo, hi int) {
		for j := lo; j < hi; j++ {
			cells := src[j*sw : (j+1)*sw]
			for y := 2 * j; y < min(2*j+2, h); y++ {
				near := src[max(j-1, 0)*sw : max(j-1, 0)*sw+sw]
				if y == 2*j+1 {
					near = src[min(j+1, sh-1)*sw : min(j+1, sh-1)*sw+sw]
				}
				out := pix[y*stride : y*stride+w]
				p := mix4(near[0], cells[0])
				c := p
				for i := range sw {
					q := c
					if i+1 < sw {
						q = mix4(near[i+1], cells[i+1])
					}
					a, b := pixel(mix4(p, c)), pixel(mix4(q, c))
					if x := 2 * i; x+1 < w {
						if over < 256 {
							a, b = blend(out[x], a, over), blend(out[x+1], b, over)
						}
						out[x], out[x+1] = a, b
					} else if x < w {
						if over < 256 {
							a = blend(out[x], a, over)
						}
						out[x] = a
					}
					p, c = c, q
				}
			}
		}
	})
}
