//go:build !dot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// The drawn dashboard's layout: sections in columns, the way Home Assistant lays a sections view out,
// each section going under whichever column is shortest so far, and each block of a section drawn as
// Home Assistant draws its card - a tile, a card of rows with switches, text, a graph, a gauge, a
// picture - in the colors of the house's own theme.

// Sizes, in the sizes the rest of the package is written in (a Show 5 in landscape).
const (
	dashSide    = 20  // the page's margin
	dashGap     = 12  // between sections, and between blocks and tiles
	sectionMinW = 360 // a column is at least this wide, so a Show 5 has two
	tileH       = 84
	headingH    = 46
	rowH        = 52
	cardPad     = 14
	cardTitleH  = 40
	textLineH   = 30
	graphH      = 150
	gaugeH      = 172
)

// dashPal is the page's colors: the house's theme, or the device's own when there is none.
type dashPal struct {
	bg, card, text, sub, accent, active color.RGBA
	rad                                 float64
}

func (r *paint) palette(t dashboard.Theme) dashPal {
	if !t.Set {
		return dashPal{bg: walnut, card: surface(3), text: cream, sub: dim, accent: amber, active: amber, rad: r.sf(18)}
	}
	return dashPal{bg: t.Background, card: t.Card, text: t.Text, sub: t.Sub, accent: t.Accent, active: t.Active,
		rad: float64(r.s(t.Radius))}
}

// dashPage draws a drawn dashboard into area, scrolled down by scroll: the whole panel on the Show,
// the part of the Spot's round one a column fits in.
func (r *paint) dashPage(v dashboard.Drawn, scroll int, adj dashAdjusting, area image.Rectangle) {
	pal := r.palette(v.Theme)
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(pal.bg), image.Point{}, draw.Src)
	fc := r.faces()
	if len(v.Sections) == 0 {
		msg := v.Problem
		if msg == "" {
			msg = "Loading the dashboard…"
		}
		lines := r.wrapLines(fc.label, msg, area.Dx())
		y := area.Min.Y + area.Dy()/2 - len(lines)*r.s(30)/2
		for _, line := range lines {
			y += r.s(30)
			r.text(fc.label, line, area.Min.X+(area.Dx()-r.width(fc.label, line))/2, y, pal.sub)
		}
		r.setDash(nil, 0)
		return
	}

	side, gap := r.s(dashSide), r.s(dashGap)
	width := area.Dx() - 2*side
	cols := max(1, (width+gap)/(r.s(sectionMinW)+gap))
	colW := (width - gap*(cols-1)) / cols
	heights := make([]int, cols)
	var tiles []dashTile
	for _, sec := range v.Sections {
		c := 0
		for i := range heights {
			if heights[i] < heights[c] {
				c = i
			}
		}
		x := area.Min.X + side + c*(colW+gap)
		y := heights[c] + gap
		h := r.section(sec, x, area.Min.Y+y-scroll, colW, pal, adj, &tiles)
		heights[c] = y + h
	}
	most := 0
	for _, h := range heights {
		most = max(most, h)
	}
	// How far the page can scroll: its content, and the part of the panel below the area.
	r.setDash(tiles, most+gap+(r.h-area.Dy()))
}

// setDash keeps where the tiles were drawn and how far the page scrolls, for the touch goroutine to
// read: under zmu, as the settings screen's tap zones are.
func (r *paint) setDash(tiles []dashTile, content int) {
	r.zmu.Lock()
	r.dashTiles, r.dashContent = tiles, content
	r.zmu.Unlock()
}

// dash is what setDash kept.
func (r *paint) dash() ([]dashTile, int) {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return r.dashTiles, r.dashContent
}

// section draws a section's blocks down from y, and says how tall they came to. Blocks entirely off
// the screen are measured and not drawn.
func (r *paint) section(sec dashboard.Section, x, y, w int, pal dashPal, adj dashAdjusting, tiles *[]dashTile) int {
	top := y
	gap := r.s(dashGap)
	for i, b := range sec.Blocks {
		if i > 0 {
			y += gap
		}
		y += r.block(b, x, y, w, pal, adj, tiles)
	}
	return y - top
}

func (r *paint) visible(top, bottom int) bool { return bottom > 0 && top < r.h }

// block draws one block at x, y, w wide, and says how tall it is.
func (r *paint) block(b dashboard.Block, x, y, w int, pal dashPal, adj dashAdjusting, tiles *[]dashTile) int {
	fc := r.faces()
	switch {
	case b.Heading != "":
		h := r.s(headingH)
		if r.visible(y, y+h) {
			base := y + h - r.s(12)
			name := b.Heading
			room := w
			if b.Right != "" {
				rw := r.width(fc.value, b.Right)
				r.text(fc.value, b.Right, x+w-rw, base, pal.sub)
				room -= rw + r.s(16)
			}
			r.text(fc.header, r.fit(fc.header, name, room), x+r.s(4), base, pal.text)
		}
		return h

	case len(b.Tiles) > 0:
		gap := r.s(dashGap)
		// Two tiles abreast where there is room for two, as a section has on the Show; one in the
		// Spot's narrow column.
		per := 1
		if w >= r.s(380) {
			per = 2
		}
		tw := (w - gap*(per-1)) / per
		th := r.s(tileH)
		rows := (len(b.Tiles) + per - 1) / per
		for i, t := range b.Tiles {
			box := image.Rect(x+(i%per)*(tw+gap), y+(i/per)*(th+gap), x+(i%per)*(tw+gap)+tw, y+(i/per)*(th+gap)+th)
			if r.visible(box.Min.Y, box.Max.Y) {
				r.tile(box, t, pal, adj)
				r.zone(tiles, box, t)
			}
		}
		return rows*th + (rows-1)*gap

	case len(b.Rows) > 0 || (b.Title != "" && len(b.Text) == 0 && b.Graph == nil && b.Gauge == nil && b.Picture == nil):
		pad := r.s(cardPad)
		h := pad*2 + len(b.Rows)*r.s(rowH)
		if b.Title != "" {
			h += r.s(cardTitleH)
		}
		if !r.visible(y, y+h) {
			return h
		}
		card := image.Rect(x, y, x+w, y+h)
		r.roundFill(card, pal.rad, pal.card, pal.card)
		ry := y + pad
		if b.Title != "" {
			r.text(fc.labelBold, r.fit(fc.labelBold, b.Title, w-2*pad), x+pad+r.s(4), ry+r.s(28), pal.text)
			ry += r.s(cardTitleH)
		}
		for _, t := range b.Rows {
			row := image.Rect(x, ry, x+w, ry+r.s(rowH))
			if r.visible(row.Min.Y, row.Max.Y) {
				r.row(row, t, pal, adj)
				r.zone(tiles, row, t)
			}
			ry += r.s(rowH)
		}
		return h

	case len(b.Text) > 0:
		pad := r.s(cardPad) + r.s(4)
		var lines []string
		for _, p := range b.Text {
			lines = append(lines, r.wrapLines(fc.label, p, w-2*pad)...)
		}
		h := pad*2 + len(lines)*r.s(textLineH)
		if b.Title != "" {
			h += r.s(cardTitleH)
		}
		if !r.visible(y, y+h) {
			return h
		}
		r.roundFill(image.Rect(x, y, x+w, y+h), pal.rad, pal.card, pal.card)
		ty := y + pad
		if b.Title != "" {
			r.text(fc.labelBold, r.fit(fc.labelBold, b.Title, w-2*pad), x+pad, ty+r.s(26), pal.text)
			ty += r.s(cardTitleH)
		}
		for _, line := range lines {
			ty += r.s(textLineH)
			r.text(fc.label, line, x+pad, ty-r.s(8), pal.text)
		}
		return h

	case b.Graph != nil:
		h := r.s(graphH)
		if r.visible(y, y+h) {
			r.graphCard(image.Rect(x, y, x+w, y+h), *b.Graph, pal)
		}
		return h

	case b.Gauge != nil:
		h := r.s(gaugeH)
		if r.visible(y, y+h) {
			r.gaugeCard(image.Rect(x, y, x+w, y+h), *b.Gauge, pal)
		}
		return h

	case b.Picture != nil:
		h := w * 9 / 16
		if img := b.Picture.Image; img != nil && img.Bounds().Dx() > 0 {
			h = w * img.Bounds().Dy() / img.Bounds().Dx()
		}
		h = min(h, r.s(320))
		if r.visible(y, y+h) {
			r.pictureCard(image.Rect(x, y, x+w, y+h), *b.Picture, pal)
		}
		return h
	}
	return 0
}

// zone remembers where a tile or a row is, for a tap or a slide to find it.
func (r *paint) zone(tiles *[]dashTile, box image.Rectangle, t dashboard.Tile) {
	if t.Tap != nil || t.Adjust != nil {
		*tiles = append(*tiles, dashTile{r: box, action: t.Tap, adjust: t.Adjust})
	}
}

// mdiIcon draws an mdi icon with its top left at x, y, size tall.
func (r *paint) mdiIcon(name string, x, y, size int, c color.RGBA) {
	face := r.iconFace(size)
	g, ok := mdi.Rune(name)
	if face == nil || !ok {
		return
	}
	r.text(face, string(g), x, y+r.s(size)-r.s(2), c)
}

// tile draws one thing as Home Assistant's tile card does: a round badge with its icon, lit when it
// is on, then its name, and what it is doing underneath.
func (r *paint) tile(b image.Rectangle, t dashboard.Tile, pal dashPal, adj dashAdjusting) {
	fc := r.faces()
	r.roundFill(b, pal.rad, pal.card, pal.card)

	// A level: a bar along the foot of the tile showing where it is, which is also the sign that a
	// finger can slide it. While one does, the whole tile fills to the level instead.
	sliding := t.Adjust != nil && adj.entity == t.Adjust.Entity
	if a := t.Adjust; a != nil && a.Max > a.Min {
		v := a.Value
		if sliding {
			v = adj.value
		}
		frac := min(max((v-a.Min)/(a.Max-a.Min), 0), 1)
		if sliding {
			fill := image.Rect(b.Min.X, b.Min.Y, b.Min.X+int(float64(b.Dx())*frac), b.Max.Y)
			if fill.Dx() > 0 {
				r.roundFill(fill, pal.rad, lerp(pal.card, pal.active, 0.35), lerp(pal.card, pal.active, 0.35))
			}
			t.Value = a.Label(v)
		} else if frac > 0 && t.On {
			inset := r.s(14)
			bar := image.Rect(b.Min.X+inset, b.Max.Y-r.s(7), b.Min.X+inset+int(float64(b.Dx()-2*inset)*frac), b.Max.Y-r.s(4))
			if bar.Dx() > 0 {
				r.roundFill(bar, r.sf(2), pal.active, pal.active)
			}
		}
	}

	pad := r.s(12)
	badge := r.s(44)
	bx, by := b.Min.X+pad, b.Min.Y+(b.Dy()-badge)/2
	ring := image.Rect(bx, by, bx+badge, by+badge)
	ic := pal.sub
	switch {
	case t.Gone:
		ic = lerp(pal.sub, pal.card, 0.5)
		r.roundFill(ring, float64(badge)/2, lerp(pal.card, pal.sub, 0.12), lerp(pal.card, pal.sub, 0.12))
	case t.On:
		ic = pal.active
		r.roundFill(ring, float64(badge)/2, lerp(pal.card, pal.active, 0.22), lerp(pal.card, pal.active, 0.22))
	default:
		r.roundFill(ring, float64(badge)/2, lerp(pal.card, pal.sub, 0.14), lerp(pal.card, pal.sub, 0.14))
	}
	isz := 26
	r.mdiIcon(t.Icon, bx+(badge-r.s(isz))/2, by+(badge-r.s(isz))/2, isz, ic)

	x := bx + badge + r.s(12)
	room := b.Max.X - pad - x
	r.text(fc.label, r.fit(fc.label, t.Name, room), x, b.Min.Y+b.Dy()/2-r.s(3), pal.text)
	r.text(fc.sub, r.fit(fc.sub, t.Value, room), x, b.Min.Y+b.Dy()/2+r.s(24), pal.sub)
}

// row draws one line of an entities card: icon, name, and at the end a switch or the state.
func (r *paint) row(b image.Rectangle, t dashboard.Tile, pal dashPal, adj dashAdjusting) {
	fc := r.faces()
	pad := r.s(cardPad) + r.s(4)
	isz := 28
	ic := pal.sub
	if t.On {
		ic = pal.active
	}
	if t.Gone {
		ic = lerp(pal.sub, pal.card, 0.5)
	}
	r.mdiIcon(t.Icon, b.Min.X+pad, b.Min.Y+(b.Dy()-r.s(isz))/2, isz, ic)
	x := b.Min.X + pad + r.s(isz) + r.s(18)
	right := b.Max.X - pad

	value := t.Value
	if t.Adjust != nil && adj.entity == t.Adjust.Entity {
		value = t.Adjust.Label(adj.value)
	}
	switch {
	case t.Switch && !(t.Adjust != nil && adj.entity == t.Adjust.Entity):
		sw := image.Rect(right-r.s(46), b.Min.Y+(b.Dy()-r.s(24))/2, right, b.Min.Y+(b.Dy()+r.s(24))/2)
		track := lerp(pal.card, pal.sub, 0.35)
		if t.On {
			track = lerp(pal.card, pal.accent, 0.6)
		}
		r.roundFill(sw, float64(sw.Dy())/2, track, track)
		knob := sw.Dy() - r.s(6)
		kx := sw.Min.X + r.s(3)
		if t.On {
			kx = sw.Max.X - r.s(3) - knob
		}
		kc := lerp(pal.text, pal.card, 0.1)
		if t.On {
			kc = pal.accent
		}
		k := image.Rect(kx, sw.Min.Y+r.s(3), kx+knob, sw.Min.Y+r.s(3)+knob)
		r.roundFill(k, float64(knob)/2, kc, kc)
		right = sw.Min.X - r.s(12)
	default:
		vw := r.width(fc.value, value)
		vw = min(vw, (right-x)/2)
		r.text(fc.value, r.fit(fc.value, value, vw), right-vw, b.Min.Y+b.Dy()/2+r.s(10), pal.sub)
		right -= vw + r.s(12)
	}
	r.text(fc.label, r.fit(fc.label, t.Name, right-x), x, b.Min.Y+b.Dy()/2+r.s(10), pal.text)
}

// graphCard draws a sensor card: its icon, name and reading, and its recent history as a line with
// the ground under it tinted.
func (r *paint) graphCard(b image.Rectangle, g dashboard.Graph, pal dashPal) {
	fc := r.faces()
	r.roundFill(b, pal.rad, pal.card, pal.card)
	pad := r.s(cardPad) + r.s(4)
	r.mdiIcon(g.Icon, b.Min.X+pad, b.Min.Y+pad, 26, pal.sub)
	r.text(fc.label, r.fit(fc.label, g.Name, b.Dx()-2*pad-r.s(40)), b.Min.X+pad+r.s(38), b.Min.Y+pad+r.s(22), pal.sub)
	r.text(fc.header, g.Value, b.Min.X+pad, b.Min.Y+pad+r.s(66), pal.text)

	area := image.Rect(b.Min.X+r.s(6), b.Min.Y+pad+r.s(78), b.Max.X-r.s(6), b.Max.Y-r.s(10))
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range g.Points {
		if !math.IsNaN(v) {
			lo, hi = min(lo, v), max(hi, v)
		}
	}
	if math.IsInf(lo, 1) {
		return
	}
	if hi-lo < 1e-9 {
		lo, hi = lo-1, hi+1
	}
	n := len(g.Points)
	yAt := func(v float64) int {
		return area.Max.Y - int((v-lo)/(hi-lo)*float64(area.Dy()-r.s(4))) - r.s(2)
	}
	// color.RGBA is premultiplied: a translucent color has its channels scaled by its alpha.
	const tint = 48
	fill := color.RGBA{uint8(int(pal.accent.R) * tint / 255), uint8(int(pal.accent.G) * tint / 255), uint8(int(pal.accent.B) * tint / 255), tint}
	thick := max(r.s(3), 2)
	prev := -1
	for px := area.Min.X; px < area.Max.X; px++ {
		// The value at this column, between the two points either side of it.
		f := float64(px-area.Min.X) / float64(max(area.Dx()-1, 1)) * float64(n-1)
		i := int(f)
		a, c := g.Points[i], g.Points[min(i+1, n-1)]
		if math.IsNaN(a) || math.IsNaN(c) {
			prev = -1
			continue
		}
		v := a + (c-a)*(f-float64(i))
		y := yAt(v)
		draw.Draw(r.dst, image.Rect(px, y, px+1, area.Max.Y), image.NewUniform(fill), image.Point{}, draw.Over)
		top, bottom := y, y
		if prev >= 0 {
			top, bottom = min(prev, y), max(prev, y)
		}
		draw.Draw(r.dst, image.Rect(px, top-thick/2, px+1, bottom+thick-thick/2), image.NewUniform(pal.accent), image.Point{}, draw.Src)
		prev = y
	}
}

// severity colors are Home Assistant's own for a gauge's bands.
var severity = map[string]color.RGBA{
	"green":  {0x43, 0xa0, 0x47, 255},
	"yellow": {0xff, 0xa6, 0x00, 255},
	"red":    {0xdb, 0x44, 0x37, 255},
}

// gaugeCard draws a gauge card: a half ring filled as far as the reading goes, the reading in its
// middle and the name under it.
func (r *paint) gaugeCard(b image.Rectangle, g dashboard.Gauge, pal dashPal) {
	fc := r.faces()
	r.roundFill(b, pal.rad, pal.card, pal.card)
	outer := float64(min(b.Dx()/2-r.s(24), r.s(84)))
	thick := outer * 0.24
	cx := float64(b.Min.X) + float64(b.Dx())/2
	cy := float64(b.Min.Y) + float64(r.s(20)) + outer
	on, ok := severity[g.Severity]
	if !ok {
		on = pal.accent
	}
	track := lerp(pal.card, pal.sub, 0.25)
	for py := int(cy - outer - 1); py <= int(cy)+1; py++ {
		for px := int(cx - outer - 1); px <= int(cx+outer)+1; px++ {
			dx, dy := float64(px)+0.5-cx, float64(py)+0.5-cy
			if dy > 0.5 {
				continue
			}
			d := math.Hypot(dx, dy)
			cover := min(clamp01(outer-d+0.5), clamp01(d-(outer-thick)+0.5))
			if cover <= 0 || !image.Pt(px, py).In(r.dst.Rect) {
				continue
			}
			angle := math.Atan2(-dy, dx) // π at the left end, 0 at the right
			c := track
			if angle >= math.Pi*(1-g.Frac) {
				c = on
			}
			i := r.dst.PixOffset(px, py)
			cur := color.RGBA{r.dst.Pix[i], r.dst.Pix[i+1], r.dst.Pix[i+2], 255}
			mixed := lerp(cur, c, cover)
			r.dst.Pix[i], r.dst.Pix[i+1], r.dst.Pix[i+2] = mixed.R, mixed.G, mixed.B
		}
	}
	vw := r.width(fc.header, g.Value)
	r.text(fc.header, g.Value, int(cx)-vw/2, int(cy)-r.s(4), pal.text)
	name := r.fit(fc.label, g.Name, b.Dx()-2*r.s(cardPad))
	r.text(fc.label, name, int(cx)-r.width(fc.label, name)/2, b.Max.Y-r.s(18), pal.sub)
}

// pictureCard draws a camera's snapshot or a picture, filling the card, with its name along the foot.
func (r *paint) pictureCard(b image.Rectangle, p dashboard.Picture, pal dashPal) {
	fc := r.faces()
	r.roundFill(b, pal.rad, pal.card, pal.card)
	if p.Image != nil {
		inner := b.Inset(r.s(2))
		xdraw.ApproxBiLinear.Scale(r.dst, inner, p.Image, p.Image.Bounds(), draw.Src, nil)
	} else {
		msg := "Loading the picture…"
		if p.TooLarge {
			msg = "Too large to show here"
		}
		r.text(fc.sub, msg, b.Min.X+(b.Dx()-r.width(fc.sub, msg))/2, b.Min.Y+b.Dy()/2, pal.sub)
	}
	if p.Name != "" {
		band := image.Rect(b.Min.X, b.Max.Y-r.s(40), b.Max.X, b.Max.Y)
		draw.Draw(r.dst, band, image.NewUniform(color.RGBA{0, 0, 0, 140}), image.Point{}, draw.Over)
		r.text(fc.label, r.fit(fc.label, p.Name, b.Dx()-2*r.s(cardPad)), b.Min.X+r.s(cardPad), b.Max.Y-r.s(12), color.RGBA{255, 255, 255, 255})
	}
}
