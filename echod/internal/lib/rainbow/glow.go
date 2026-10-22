package rainbow

import (
	"image"
	"math"
	"time"
)

// The light round the edge. Seven soft blobs travel round it, each swelling and shrinking, and the
// light at a point is how much of them is there, falling off inward from the edge: further and
// stronger the louder the voice, and further along the top and bottom than the sides, which is what
// stretches it sideways. While the device thinks it is along the top and bottom alone, narrower and
// quicker. Its colors are a loop round the screen twice, sliding slowly; where it is brightest it goes
// to white. Along the edge itself runs a line of the same colors, thicker where the blobs are, with a
// soft glow of its own, and a faint rim.

// edgePalette is the loop of colors: pink, rose, violet, blue, cyan, orange and back to pink.
var edgePalette = [][3]float32{{255, 40, 200}, {255, 45, 120}, {140, 50, 255}, {40, 90, 255}, {0, 200, 255}, {255, 110, 20}, {255, 40, 200}}

// lineLeast is how thin the line along the edge gets as the light goes, of its width.
const lineLeast = 0.6

// blobCount is how many blobs travel round, and along how many steps round the edge the blobs and the
// colors are worked out each frame.
const (
	blobCount = 7
	along     = 1024
)

// glowBufs is the light's working memory: the soft glow at a quarter of the screen's size and the
// whole light at half, RGB, and what each frame works out along the edge.
type glowBufs struct {
	soft, half []uint32          // lanes (double.go)
	amount     [along]float32    // how much blob there is at each step
	color      [along][3]float32 // and the color there
	color8     [along][3]int32   // the same, 0 to 255 as integers
	think      float64           // how far the light has gone over to thinking, 0 to 1
	blobAt     float64           // how far round the blobs have traveled
	colorAt    float64           // how far the colors have slid
	fr         glowFrame         // the frame's numbers
}

// glowFrame is the frame's numbers for the light: how far it has gone over to thinking, the entry's
// push, the voice, the light's gain and strength, half a cell and the rim's strength.
type glowFrame struct {
	tm, boost, amp, gain, vis, cell, rimA float32
}

// expTable is e^-x for x from 0 to 32 in steps of 1/128; cdfTable the normal distribution from -6 to
// 6 in steps of 1/64, for a blurred edge.
var expTable, cdfTable = func() (e, c []float32) {
	e = make([]float32, 32*128+1)
	for i := range e {
		e[i] = float32(math.Exp(-float64(i) / 128))
	}
	c = make([]float32, 12*64+1)
	for i := range c {
		c[i] = float32(0.5 * math.Erfc(-(float64(i)/64-6)/math.Sqrt2))
	}
	return e, c
}()

func expNeg(x float32) float32 {
	if x >= 32 {
		return 0
	}
	return expTable[int(x*128)]
}

func cdf(z float32) float32 {
	switch {
	case z <= -6:
		return 0
	case z >= 6:
		return 1
	}
	return cdfTable[int((z+6)*64)]
}

func lerp32(a, b, t float32) float32 { return a + (b-a)*t }

// glow adds the light over dst, at its strength s.vis.
func (s *Screen) glow(dst *image.RGBA, g *geometry, now time.Time, think bool) {
	s.glowBegin(g, now, think)
	s.glowEnd(dst, g)
}

// glowBegin works out the frame's light along the edge; glowEnd draws it and adds it over dst.
func (s *Screen) glowBegin(g *geometry, now time.Time, think bool) {
	b := &s.glowBufs
	if len(b.soft) != g.qw*g.qh {
		b.soft, b.half = make([]uint32, g.qw*g.qh), make([]uint32, g.hw*g.hh)
	}
	t := float64(now.UnixMilli()%(1<<30)) / 1000

	// Thinking comes in and goes softly rather than at once.
	to := 0.0
	if think {
		to = 1
	}
	b.think += (to - b.think) * rate(0.12, s.step)

	ampN := max(0.15+0.85*s.lvl, s.boost)
	ampT := 0.5 + 0.2*math.Sin(t*3.1)
	amp := ampN + (ampT-ampN)*b.think
	b.blobAt += (0.035 + (0.12-0.035)*b.think) * s.step
	b.colorAt += (0.12 + (0.35-0.12)*b.think) * s.step
	sig2 := 0.0022 + (0.0012-0.0022)*b.think

	var pos, size [blobCount]float64
	for k := range blobCount {
		pos[k] = math.Mod(float64(k)/blobCount+b.blobAt, 1)
		size[k] = (0.45 + 0.55*math.Sin(t*1.9+float64(k)*2.3)) * 0.9
	}
	pn := float64(len(edgePalette) - 1)
	for i := range along {
		u := (float64(i) + 0.5) / along
		a := 0.3
		for k := range blobCount {
			du := math.Abs(u - pos[k])
			if du > 0.5 {
				du = 1 - du
			}
			a += size[k] * float64(expNeg(float32(du*du/sig2)))
		}
		b.amount[i] = float32(a)
		p := u*2 - b.colorAt
		p = (p - math.Floor(p)) * pn
		n := int(p)
		f := float32(p - float64(n))
		c0, c1 := edgePalette[n], edgePalette[n+1]
		b.color[i] = [3]float32{lerp32(c0[0], c1[0], f), lerp32(c0[1], c1[1], f), lerp32(c0[2], c1[2], f)}
		b.color8[i] = [3]int32{int32(b.color[i][0]), int32(b.color[i][1]), int32(b.color[i][2])}
	}

	cell := float32(2 / g.k) // a half cell, in design units
	b.fr = glowFrame{
		tm: float32(b.think), boost: float32(s.boost), amp: float32(amp), gain: float32((0.6 + 1.2*amp) * s.vis),
		vis: float32(s.vis), cell: cell, rimA: float32((0.18 + 0.3*amp) * s.vis * 255),
	}
}

func (s *Screen) glowEnd(dst *image.RGBA, g *geometry) {
	s.glowHere(g)
	addUp2(dst, s.glowBufs.half, g.hw, g.hh, g.inner, g.soft2)
}

// corners rounds the screen's corners, as far as on is (0 to 1), a pixel at a time at the screen's
// own size so their curve is smooth.
func corners(dst *image.RGBA, g *geometry, on float64) {
	v := uint32(min(on, 1)*256 + 0.5)
	if v == 0 {
		return
	}
	pix := dst.Pix
	for _, c := range g.corner {
		f := 256 - v*(256-uint32(c.keep))/256
		p := pix[c.at : c.at+3 : c.at+3]
		p[0] = uint8(uint32(p[0]) * f >> 8)
		p[1] = uint8(uint32(p[1]) * f >> 8)
		p[2] = uint8(uint32(p[2]) * f >> 8)
	}
}

// glowHere draws the light into b.half.
func (s *Screen) glowHere(g *geometry) {
	b := &s.glowBufs
	fr := b.fr
	tm, boost, a32, gain, vis, cell, rimA := fr.tm, fr.boost, fr.amp, fr.gain, fr.vis, fr.cell, fr.rimA
	// light is the glow at a cell, and the color it is in.
	light := func(c *edgeCell) (float32, *[3]float32, float32, float32) {
		wE := c.wEn + (c.wEt-c.wEn)*tm + boost*c.nx75
		reach := 10 + a32*c.rA
		reach += (c.rT-reach)*tm + boost*c.rB
		fall := expNeg(c.d/reach) + c.near
		blob := b.amount[c.ui]
		return wE * blob * gain * fall * c.wn, &b.color[c.ui], wE, blob
	}
	// glowOnly is shade far in from the edge, where only the glow reaches: in whole numbers, which the
	// Show's processor works out far faster.
	glowOnly := func(c *edgeCell) uint32 {
		wE := c.wEn + (c.wEt-c.wEn)*tm + boost*c.nx75
		reach := 10 + a32*c.rA
		reach += (c.rT-reach)*tm + boost*c.rB
		i := int32(wE * b.amount[c.ui] * gain * expNeg(c.d/reach) * c.wn * 256)
		if i <= 0 {
			return 0
		}
		col := &b.color8[c.ui]
		wh := max(0, i-256) * 120 >> 8
		return uint32(min(255, col[0]*i>>8+wh)) | uint32(min(255, col[1]*i>>8+wh))<<10 | uint32(min(255, col[2]*i>>8+wh))<<20
	}
	// shade is a cell's light, as lanes: the glow, the line along the edge, 2w wide and centered on
	// it so that w of it is inside, with its glow (the line blurred by 4), and the rim. The same at
	// either size, so the two grids meet without a seam; far in, only the glow is left of it.
	shade := func(c *edgeCell) uint32 {
		i, _, wE, blob := light(c)
		// Thinner as the light comes on and goes, but no thinner than 0.6 of itself: a line thinner than
		// a pixel is drawn in steps, and it goes out by its strength instead.
		w := (1.4 + (2+4.5*a32)*wE*minf(1.4, blob)) * maxf(vis, lineLeast)
		cover := clampf((w-c.d)/cell + 0.5)
		// In 256ths from here: the line's strength, how far its color goes to white, the glow's.
		line := int32(vis * (cover + 0.7*(cdf((w-c.d)/4)-cdf((-w-c.d)/4))) * 256)
		white := int32(clampf(blob-0.8) * (0.35 * 256))
		i8 := int32(i * 256)
		wh := max(0, i8-256)*120>>8 + int32(rimA*clampf((1-c.d)/cell+0.5))
		col := &b.color8[c.ui]
		var out uint32
		for ch, at := range [3]uint{0, 10, 20} {
			cc := col[ch]
			lc := cc + (255-cc)*white>>8
			out |= uint32(min(255, min(255, cc*i8>>8+wh)+lc*line>>8)) << at
		}
		return out
	}

	soft := g.soft
	par(len(soft), func(lo, hi int) {
		for n := lo; n < hi; n++ {
			if c := &soft[n]; c.far {
				b.soft[c.at] = glowOnly(c)
			} else {
				b.soft[c.at] = shade(c)
			}
		}
	})
	up2(b.soft, g.qw, g.qh, b.half, g.hw, g.hh, g.inner, g.soft2)

	// The sharp part, at half size, close to the edge.
	sharp := g.sharp
	par(len(sharp), func(lo, hi int) {
		for n := lo; n < hi; n++ {
			b.half[sharp[n].at] = shade(&sharp[n])
		}
	})
}
