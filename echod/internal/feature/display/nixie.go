//go:build !dot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/font"
)

// The Nixie clock style's tubes, on the Show and the Spot alike: each figure in a glass tube of its
// own, lit warm orange in front of the other nine, which stand faintly behind it as unlit wire, all
// under a fine mesh; the colon is a slim tube with two neon dots. The tubes keep their own colors
// whatever the theme: the glow is the point of them. Working out a tube's glow takes a pass over every
// pixel of it, and the clock is drawn every second, so each tube is drawn once for its size and figure
// and kept; a frame only lays down the ones it shows.

// nixiePt is a point: of a figure's wire across (0 left, 1 right) and down (0 top, 1 bottom) its box,
// or of a tube, in its pixels.
type nixiePt struct{ x, y float64 }

// nixieArc is the part of an ellipse centered at cx, cy from angle a0 to a1, in degrees (0 to the
// right, 90 down), as points along it.
func nixieArc(cx, cy, rx, ry, a0, a1 float64) []nixiePt {
	n := max(int(math.Abs(a1-a0)/10), 2)
	out := make([]nixiePt, n+1)
	for i := range out {
		a := (a0 + (a1-a0)*float64(i)/float64(n)) * math.Pi / 180
		out[i] = nixiePt{cx + rx*math.Cos(a), cy + ry*math.Sin(a)}
	}
	return out
}

// nixieFigures are the ten figures' wires, each a stroke or two of points, thin and tall as a tube's
// cathodes are bent.
var nixieFigures = [10][][]nixiePt{
	{nixieArc(0.5, 0.5, 0.5, 0.5, 0, 360)},
	{{{0.28, 0.14}, {0.56, 0}, {0.56, 1}}},
	{append(nixieArc(0.5, 0.26, 0.48, 0.26, 175, 395), nixiePt{0, 1}, nixiePt{1, 1})},
	{nixieArc(0.5, 0.25, 0.44, 0.24, 200, 450), nixieArc(0.5, 0.745, 0.5, 0.255, -90, 160)},
	{{{0.74, 1}, {0.74, 0}, {0, 0.68}, {1, 0.68}}},
	{append([]nixiePt{{0.92, 0}, {0.14, 0}, {0.17, 0.46}}, nixieArc(0.5, 0.69, 0.5, 0.31, -128, 155)...)},
	{nixieArc(0.5, 0.69, 0.5, 0.31, 0, 360), nixieArc(1, 0.69, 1, 0.69, 180, 256)},
	{{{0, 0}, {1, 0}, {0.3, 1}}},
	{nixieArc(0.5, 0.235, 0.42, 0.235, 0, 360), nixieArc(0.5, 0.735, 0.5, 0.265, 0, 360)},
	{nixieArc(0.5, 0.31, 0.5, 0.31, 0, 360), nixieArc(0, 0.31, 1, 0.69, 0, 76)},
}

// nixieDepth is how far back each figure stands in the stack, front first, as in a real tube; those
// further back are a little smaller.
var nixieDepth = [10]int{5, 0, 2, 9, 6, 4, 1, 3, 8, 7}

// The tube's colors: the lit wire, its hot middle, the glow round it, and the unlit wire's copper.
var (
	nixieLit    = [3]float64{255, 138, 42}
	nixieHot    = [3]float64{255, 228, 170}
	nixieGlow   = [3]float64{255, 84, 14}
	nixieCopper = [3]float64{100, 62, 40}
)

// nixieShape is where in a tube h tall the figures go: their box's top left, width and height. A
// digit's tube is nixieWide of its height across; the figures are a little over half its height, set
// a little above the middle, clear of the socket at the foot.
func nixieShape(w, h int) (x, y, fw, fh float64) {
	fh = float64(h) * 0.56
	fw = fh * 0.56
	return (float64(w) - fw) / 2, float64(h)*0.45 - fh/2, fw, fh
}

// nixieWide is a digit's tube's width for its height, and nixieSlim the colon's.
const (
	nixieWide = 0.56
	nixieSlim = 0.3
)

// figureWires is figure's wires in a tube w by h, in its pixels, a little smaller the further back it
// stands.
func figureWires(figure, w, h int) [][]nixiePt {
	x, y, fw, fh := nixieShape(w, h)
	k := 1 - 0.018*float64(nixieDepth[figure])
	cx, cy := x+fw/2, y+fh/2
	var out [][]nixiePt
	for _, stroke := range nixieFigures[figure] {
		pts := make([]nixiePt, len(stroke))
		for i, p := range stroke {
			pts[i] = nixiePt{cx + (p.x-0.5)*fw*k, cy + (p.y-0.5)*fh*k}
		}
		out = append(out, pts)
	}
	return out
}

// nixieNear is, for each pixel of a w by h tube, how far it is from the nearest of the wires, up to
// reach: further than that is reach. Worked a stroke at a time, over only the pixels within reach of
// it.
func nixieNear(w, h int, wires [][]nixiePt, reach float64) []float32 {
	d := make([]float32, w*h)
	for i := range d {
		d[i] = float32(reach)
	}
	seg := func(a, b nixiePt) {
		dx, dy := b.x-a.x, b.y-a.y
		l2 := dx*dx + dy*dy
		x0, x1 := max(int(math.Min(a.x, b.x)-reach), 0), min(int(math.Max(a.x, b.x)+reach)+1, w)
		y0, y1 := max(int(math.Min(a.y, b.y)-reach), 0), min(int(math.Max(a.y, b.y)+reach)+1, h)
		for y := y0; y < y1; y++ {
			py := float64(y) + 0.5
			for x := x0; x < x1; x++ {
				px := float64(x) + 0.5
				t := 0.0
				if l2 > 0 {
					t = clamp01(((px-a.x)*dx + (py-a.y)*dy) / l2)
				}
				if v := float32(math.Hypot(px-(a.x+t*dx), py-(a.y+t*dy))); v < d[y*w+x] {
					d[y*w+x] = v
				}
			}
		}
	}
	for _, stroke := range wires {
		if len(stroke) == 1 {
			seg(stroke[0], stroke[0])
		}
		for i := 1; i < len(stroke); i++ {
			seg(stroke[i-1], stroke[i])
		}
	}
	return d
}

// tubeEdge is how far (px, py) is outside a tube w by h, negative inside: round at the top, its corners
// barely so at the socket.
func tubeEdge(px, py, w, h float64) float64 {
	top, foot := w*0.42, w*0.1
	r, qy := foot, py-(h-foot)
	if py < h/2 {
		r, qy = top, top-py
	}
	qx := math.Abs(px-w/2) - (w/2 - r)
	return math.Hypot(max(qx, 0), max(qy, 0)) + min(max(qx, qy), 0) - r
}

// drawTube is a tube w by h, with lit glowing in it (wires of stroke thickness) in front of the unlit
// wires, on a light ground or a dark one. Outside its glass it is clear.
func drawTube(w, h int, lit, unlit [][]nixiePt, stroke float64, light bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fx, fy, figW, figH := nixieShape(w, h)
	fw, fh := float64(w), float64(h)
	wire := max(1, figH*0.012)
	reach := max(2, figH*0.05) // the glow's
	var near, back []float32
	if len(lit) > 0 {
		near = nixieNear(w, h, lit, reach*6)
	}
	if len(unlit) > 0 {
		back = nixieNear(w, h, unlit, wire+2)
	}
	// The mesh over the figures, its wires pitch apart, and the part of the tube it covers.
	pitch := max(3, figH/24)
	meshX0, meshX1 := max(fx-figW*0.3, fw*0.1), min(fx+figW*1.3, fw*0.9)
	meshY0, meshY1 := fy-figH*0.08, fy+figH*1.08
	// The socket at the foot.
	socket := fh * 0.91
	glass, rim := [3]float64{30, 23, 20}, [3]float64{128, 112, 100}
	if light {
		// On a light ground the glass shows its smoke and a dark edge, as a tube in front of a pale wall.
		glass, rim = [3]float64{46, 38, 34}, [3]float64{12, 9, 8}
	}
	cx, cy := fw/2, fy+figH/2
	for y := range h {
		py := float64(y) + 0.5
		for x := range w {
			px := float64(x) + 0.5
			edge := tubeEdge(px, py, fw, fh)
			cover := clamp01(0.5 - edge)
			if cover == 0 {
				continue
			}
			// Round: darker toward the sides.
			side := (px - cx) / (fw / 2)
			shade := 1 - 0.6*side*side
			var c [3]float64
			if py >= socket {
				for i := range c {
					c[i] = 52 * shade
				}
				if py < socket+max(1.5, fh*0.006) {
					for i := range c {
						c[i] += 40 * shade
					}
				}
			} else {
				for i := range c {
					c[i] = glass[i] * shade
				}
				// The unlit wires, copper, the lit one's glow over them.
				if back != nil {
					k := 0.5 * clamp01(0.5-(float64(back[y*w+x])-wire/2))
					for i := range c {
						c[i] += (nixieCopper[i] - c[i]) * k
					}
				}
				if near != nil {
					dx, dy := (px-cx)/(fw*0.5), (py-cy)/(fh*0.36)
					d := float64(near[y*w+x])
					glow := 0.16*math.Exp(-(dx*dx+dy*dy)) + 0.75*math.Exp(-d/reach) + 0.3*math.Exp(-d/(reach*3.5))
					core := clamp01(0.5 - (d - stroke/2))
					hot := clamp01(1 - d/(stroke*0.4))
					for i := range c {
						c[i] += glow * nixieGlow[i]
						in := nixieLit[i] + (nixieHot[i]-nixieLit[i])*hot
						c[i] += (in - c[i]) * core
					}
				}
				// The mesh: two sets of fine diagonal wires, darkening what is behind and catching a
				// little light themselves.
				if px > meshX0 && px < meshX1 && py > meshY0 && py < meshY1 {
					u := math.Mod(px+py, pitch)
					v := math.Mod(px-py+fh*pitch, pitch)
					m := max(clamp01(0.8-min(u, pitch-u)), clamp01(0.8-min(v, pitch-v)))
					m *= clamp01(min(px-meshX0, meshX1-px, py-meshY0, meshY1-py) / pitch)
					for i := range c {
						c[i] = c[i]*(1-0.3*m) + 10*m
					}
				}
				// The glass's sheen: a soft streak down the left, a fainter one right, a gleam on the dome.
				down := clamp01(py/(fh*0.1)) * clamp01((socket-py)/(fh*0.1))
				l, rt := (side+0.6)/0.09, (side-0.66)/0.07
				sheen := down * (0.11*math.Exp(-l*l) + 0.045*math.Exp(-rt*rt))
				gx, gy := (px-fw*0.34)/(fw*0.12), (py-fw*0.16)/(fw*0.06)
				sheen += 0.12 * math.Exp(-(gx*gx + gy*gy))
				for i := range c {
					c[i] += sheen * 255
				}
			}
			// The glass's edge.
			k := clamp01(1-math.Abs(edge+1.2)/1.2) * 0.55
			if light {
				k *= 1.5
			}
			o := img.PixOffset(x, y)
			for i := range c {
				v := c[i] + (rim[i]-c[i])*min(k, 1)
				img.Pix[o+i] = uint8(math.Min(v, 255)*cover + 0.5)
			}
			img.Pix[o+3] = uint8(255*cover + 0.5)
		}
	}
	return img
}

// nixieTubes are a renderer's tubes, kept from frame to frame: a digit's tube for each figure as it is
// first wanted, the empty one, and the colon's, all at one size; a new size or ground starts them over.
type nixieTubes struct {
	w, h  int
	light bool
	tubes [11]*image.RGBA // figures 0 to 9, then the empty tube
	colon *image.RGBA
}

// fit empties the tubes unless they are for tubes h tall on a ground as light.
func (n *nixieTubes) fit(h int, light bool) {
	if n.h != h || n.light != light {
		*n = nixieTubes{w: int(float64(h)*nixieWide + 0.5), h: h, light: light}
	}
}

// tube is the digit's tube h tall showing figure, or nothing lit (-1).
func (n *nixieTubes) tube(h, figure int, light bool) *image.RGBA {
	n.fit(h, light)
	i := 10
	if figure >= 0 && figure <= 9 {
		i = figure
	}
	if n.tubes[i] == nil {
		var lit, unlit [][]nixiePt
		for f := range nixieFigures {
			if f == figure {
				lit = figureWires(f, n.w, h)
			}
			unlit = append(unlit, figureWires(f, n.w, h)...)
		}
		_, _, _, figH := nixieShape(n.w, h)
		n.tubes[i] = drawTube(n.w, h, lit, unlit, max(1.8, figH*0.027), light)
	}
	return n.tubes[i]
}

// colonTube is the colon's slim tube h tall: two neon dots, level with a third and two thirds of the
// figures.
func (n *nixieTubes) colonTube(h int, light bool) *image.RGBA {
	n.fit(h, light)
	if n.colon == nil {
		w := int(float64(h)*nixieSlim + 0.5)
		_, y, _, figH := nixieShape(w, h)
		dots := [][]nixiePt{{{float64(w) / 2, y + figH*0.32}}, {{float64(w) / 2, y + figH*0.7}}}
		n.colon = drawTube(w, h, dots, nil, max(4, figH*0.1), light)
	}
	return n.colon
}

// nixieRow is how wide the row of tubes h tall is, with ampm in face after it: four digits' tubes and
// the colon's, a gap after each.
func (p *paint) nixieRow(h int, ampm string, face font.Face) (gap, total int) {
	tw, cw := int(float64(h)*nixieWide+0.5), int(float64(h)*nixieSlim+0.5)
	gap = max(tw/10, 2)
	total = 4*tw + cw + 4*gap
	if ampm != "" {
		total += gap + p.width(face, ampm)
	}
	return gap, total
}

// nixieFit is how tall the tubes can be in a box w by h, with ampm in face after them: the box's
// height, or less when the row would be wider than most of the box.
func (p *paint) nixieFit(w, h int, ampm string, face font.Face) int {
	gap, total := p.nixieRow(h, ampm, face)
	if limit := w * 96 / 100; total > limit {
		// Shorter tubes, the AM/PM as it was.
		aw := 0
		if ampm != "" {
			aw = gap + p.width(face, ampm)
		}
		h = int(float64(h) * float64(limit-aw) / float64(total-aw))
	}
	return h
}

// nixieClock draws the time's four figures (clockDigits: -1 is a place left empty) in tubes across box,
// as tall as fit, the colon's tube between the hours and the minutes, and ampm after them in face and
// c, all centered. It returns where the tubes went. Over a photo the first pass only notes the place,
// for the patch under it.
func (p *paint) nixieClock(n *nixieTubes, digits [4]int, ampm string, box image.Rectangle, face font.Face, c color.Color, ground color.RGBA) image.Rectangle {
	light := luma(ground.R, ground.G, ground.B) > 128
	h := p.nixieFit(box.Dx(), box.Dy(), ampm, face)
	gap, total := p.nixieRow(h, ampm, face)
	x := box.Min.X + (box.Dx()-total)/2
	top := box.Min.Y + (box.Dy()-h)/2
	left := x
	if p.over.record {
		at := image.Rect(left, top, left+total, top+h)
		p.over.note(at)
		return at
	}
	lay := func(img *image.RGBA) {
		draw.Draw(p.dst, img.Rect.Add(image.Pt(x, top)), img, image.Point{}, draw.Over)
		x += img.Rect.Dx() + gap
	}
	for i, d := range digits {
		lay(n.tube(h, d, light))
		if i == 1 {
			lay(n.colonTube(h, light))
		}
	}
	right := x - gap
	if ampm != "" {
		// Level with the figures' tops.
		_, fy, _, _ := nixieShape(0, h)
		b, _, _ := face.GlyphBounds('M')
		p.text(face, ampm, x, top+int(fy)+(-b.Min.Y).Ceil(), c)
		right = x + p.width(face, ampm)
	}
	return image.Rect(left, top, right, top+h)
}
