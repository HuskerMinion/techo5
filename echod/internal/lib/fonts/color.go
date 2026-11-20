package fonts

// Color emoji.
//
// A font of one's own may have color glyphs: the COLR table (version 0) makes a glyph a stack of other
// glyphs, bottom first, each filled with one color from the CPAL table's palette, or with the text's
// own color. The emoji font feature/cjkfont fetches is one (Fluent Emoji, tools/fonts/
// make_color_emoji_font.py). sfnt reads neither table, so they are read here, and DrawString draws
// such a glyph a layer at a time, each layer's outline filled with its color. Measuring is as before:
// a color glyph advances as its glyph does, and its bounds are its layers'.
//
// Where text is drawn as one tint (DrawStringTinted, and font.Drawer, through a face's Glyph), a color
// glyph is drawn as a mask of its layers' lightness: the text's color where the art is light, fading to
// nothing where it is dark, so it is still the emoji, in the one color.

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"
	"slices"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// textColor is the palette index that means the text's own color.
const textColor = 0xFFFF

// maxColorTable is the most of a COLR or CPAL table read: far more than an emoji font's, and a broken
// file cannot have the daemon allocate more.
const maxColorTable = 4 << 20

// colorFont is a font's COLR version 0 glyphs and its first palette.
type colorFont struct {
	bases   []colorBase // in glyph order
	layers  []colorLayer
	palette []color.NRGBA
}

// colorBase is a color glyph: its layers are layers[first : first+n].
type colorBase struct {
	glyph    sfnt.GlyphIndex
	first, n int
}

// colorLayer is one layer of a color glyph: the glyph whose outline it is, and its palette index
// (textColor for the text's color).
type colorLayer struct {
	glyph sfnt.GlyphIndex
	color uint16
}

// glyph is g's layers, bottom first, or nil when g is not a color glyph.
func (c *colorFont) glyph(g sfnt.GlyphIndex) []colorLayer {
	i, ok := slices.BinarySearchFunc(c.bases, g, func(b colorBase, g sfnt.GlyphIndex) int { return int(b.glyph) - int(g) })
	if !ok {
		return nil
	}
	b := c.bases[i]
	return c.layers[b.first : b.first+b.n]
}

// ink is what a layer of palette index i is filled with, the text drawn with src: its palette color,
// as faded as src is when src is one color; src itself for the text's color.
func (c *colorFont) ink(i uint16, src image.Image) image.Image {
	if int(i) >= len(c.palette) {
		return src
	}
	p := c.palette[i]
	if u, ok := src.(*image.Uniform); ok {
		if _, _, _, a := u.C.RGBA(); a < 0xFFFF {
			p.A = uint8(uint32(p.A) * a / 0xFFFF)
		}
	}
	return image.NewUniform(p)
}

// lightness is how light a layer of palette index i is, 0 black to 1 white, with its opacity: the
// text's color counts as white.
func (c *colorFont) lightness(i uint16) (light, opacity float32) {
	if int(i) >= len(c.palette) {
		return 1, 1
	}
	p := c.palette[i]
	return (0.2126*float32(p.R) + 0.7152*float32(p.G) + 0.0722*float32(p.B)) / 255, float32(p.A) / 255
}

// readColor reads the COLR and CPAL tables of the font file r (a collection's first font), nil with no
// error when it has no COLR table. Only COLR's version 0 glyphs are read; a later version's others
// are drawn as their glyph alone.
func readColor(r io.ReaderAt) (c *colorFont, err error) {
	defer func() {
		if p := recover(); p != nil {
			c, err = nil, errors.New("its color tables cannot be read")
		}
	}()
	colr, cpal, err := colorTables(r)
	if err != nil || colr == nil {
		return nil, err
	}
	return parseColor(colr, cpal)
}

// colorTables are the font's COLR and CPAL tables, nil for one it does not have.
func colorTables(r io.ReaderAt) (colr, cpal []byte, err error) {
	var head [12]byte
	if _, err := r.ReadAt(head[:], 0); err != nil {
		return nil, nil, err
	}
	at := int64(0)
	if string(head[:4]) == "ttcf" {
		// A collection: the first font's table directory.
		var off [4]byte
		if _, err := r.ReadAt(off[:], 12); err != nil {
			return nil, nil, err
		}
		at = int64(binary.BigEndian.Uint32(off[:]))
		if _, err := r.ReadAt(head[:], at); err != nil {
			return nil, nil, err
		}
	}
	n := int(binary.BigEndian.Uint16(head[4:]))
	dir := make([]byte, 16*n)
	if _, err := r.ReadAt(dir, at+12); err != nil {
		return nil, nil, err
	}
	read := func(rec []byte) ([]byte, error) {
		off, length := binary.BigEndian.Uint32(rec[8:]), binary.BigEndian.Uint32(rec[12:])
		if length > maxColorTable {
			return nil, errors.New("its color table is too big")
		}
		b := make([]byte, length)
		if _, err := r.ReadAt(b, int64(off)); err != nil {
			return nil, err
		}
		return b, nil
	}
	for i := 0; i < n; i++ {
		rec := dir[16*i : 16*i+16]
		switch string(rec[:4]) {
		case "COLR":
			colr, err = read(rec)
		case "CPAL":
			cpal, err = read(rec)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return colr, cpal, nil
}

var errColor = errors.New("its color tables are broken")

// parseColor reads COLR's version 0 records and CPAL's first palette (cpal may be nil: every layer
// is then in the text's color). A layer whose palette index the palette does not have is drawn in
// the text's color.
func parseColor(colr, cpal []byte) (*colorFont, error) {
	if len(colr) < 14 {
		return nil, errColor
	}
	be := binary.BigEndian
	nBase := int(be.Uint16(colr[2:]))
	nLayer := int(be.Uint16(colr[12:]))
	// The offsets are checked as 64-bit numbers: on a 32-bit board an offset past 2 GB would be negative
	// as an int, and pass a check made with ints.
	baseAt64, layerAt64 := int64(be.Uint32(colr[4:])), int64(be.Uint32(colr[8:]))
	if baseAt64+6*int64(nBase) > int64(len(colr)) || layerAt64+4*int64(nLayer) > int64(len(colr)) {
		return nil, errColor
	}
	baseAt, layerAt := int(baseAt64), int(layerAt64) // within the table, so they fit
	c := &colorFont{}
	if cpal != nil {
		pal, err := parsePalette(cpal)
		if err != nil {
			return nil, err
		}
		c.palette = pal
	}
	c.layers = make([]colorLayer, nLayer)
	for i := range c.layers {
		rec := colr[layerAt+4*i:]
		c.layers[i] = colorLayer{sfnt.GlyphIndex(be.Uint16(rec)), be.Uint16(rec[2:])}
	}
	c.bases = make([]colorBase, 0, nBase)
	for i := 0; i < nBase; i++ {
		rec := colr[baseAt+6*i:]
		b := colorBase{sfnt.GlyphIndex(be.Uint16(rec)), int(be.Uint16(rec[2:])), int(be.Uint16(rec[4:]))}
		if b.n == 0 || b.first+b.n > nLayer {
			continue // no layers, or layers the table does not have: drawn as its glyph alone
		}
		c.bases = append(c.bases, b)
	}
	// The records are in glyph order by the format's rules; sorted here should a font not keep them.
	slices.SortStableFunc(c.bases, func(a, b colorBase) int { return int(a.glyph) - int(b.glyph) })
	c.bases = slices.CompactFunc(c.bases, func(a, b colorBase) bool { return a.glyph == b.glyph })
	if len(c.bases) == 0 {
		return nil, nil
	}
	return c, nil
}

// parsePalette is CPAL's first palette.
func parsePalette(cpal []byte) ([]color.NRGBA, error) {
	if len(cpal) < 14 {
		return nil, errColor
	}
	be := binary.BigEndian
	entries, palettes := int(be.Uint16(cpal[2:])), int(be.Uint16(cpal[4:]))
	records, recordsAt64 := int(be.Uint16(cpal[6:])), int64(be.Uint32(cpal[8:]))
	if palettes == 0 {
		return nil, nil
	}
	first := int(be.Uint16(cpal[12:]))
	// The offset is checked as a 64-bit number, as parseColor's are.
	if first+entries > records || recordsAt64+4*int64(records) > int64(len(cpal)) {
		return nil, errColor
	}
	recordsAt := int(recordsAt64)
	out := make([]color.NRGBA, entries)
	for i := range out {
		rec := cpal[recordsAt+4*(first+i):]
		out[i] = color.NRGBA{R: rec[2], G: rec[1], B: rec[0], A: rec[3]} // stored blue, green, red, alpha
	}
	return out, nil
}

// colorGlyph is a character a Stack draws as a color glyph: the font and palette its layers come from,
// its glyph, its layers and its advance, at the stack's size.
type colorGlyph struct {
	font    *sfnt.Font
	color   *colorFont
	glyph   sfnt.GlyphIndex
	layers  []colorLayer
	advance fixed.Int26_6
	ppem    fixed.Int26_6
}

// maxColorEms is the most a color glyph's layers may cover together, in squares of the font's size:
// an emoji covers about one. A broken font whose layers lie far apart would otherwise have tone ask for
// a buffer of their whole span, which can be more memory than the board has.
const maxColorEms = 16

// maxColorPixels is the most a color glyph's layers may cover together at any size, in pixels.
const maxColorPixels = 1 << 22

// colorOf is r as a color glyph, if the layer that draws it draws it as one. A glyph whose layers
// together cover more than maxColorEms or maxColorPixels is not drawn as one but as its plain glyph:
// the check is made here, inside the layer's guard, so drawing in color and drawing as one tint are
// both kept within it.
func (s *Stack) colorOf(r rune) (cg colorGlyph, ok bool) {
	// ASCII is not looked up, which saves a search of the layers for every letter of a line: the Go
	// layer, ahead of every font of one's own, has a glyph for each printable ASCII character, and the
	// emoji font leaves ASCII out (tools/fonts/make_color_emoji_font.py), so none is a color glyph.
	if !s.colored || s.size <= 0 || r < 0x80 {
		return cg, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		i, has := s.find(r)
		if i < 0 || !has || s.layers[i].color == nil {
			return cg, false
		}
		l := s.layers[i]
		if s.guard(i, func() {
			g, err := glyphIndex(l.Font, &s.buf, r)
			if err != nil {
				return
			}
			if layers := l.color.glyph(g); layers != nil {
				adv, aok := l.Face.GlyphAdvance(r)
				cg = colorGlyph{l.Font, l.color, g, layers, adv, fixed.Int26_6(s.size * 64)}
				ok = aok && cg.fits()
			}
		}) {
			return cg, ok
		}
	}
}

// each calls fn with each layer's coverage mask, bottom first, placed from the glyph's origin.
func (cg colorGlyph) each(fn func(l colorLayer, mask *image.Alpha, at image.Rectangle)) {
	for _, l := range cg.layers {
		m, at := glyphMask(cg.font, l.glyph, cg.ppem)
		if m != nil {
			fn(l, m, m.Rect.Add(at))
		}
	}
}

// fits is whether the glyph's layers together cover no more than maxColorEms squares of its size and
// no more than maxColorPixels, so that drawing it as one tint asks for a buffer of a sane size.
func (cg colorGlyph) fits() bool {
	b := cg.bounds()
	area := int64(b.Dx()) * int64(b.Dy())
	em := int64(cg.ppem.Ceil())
	return area <= maxColorEms*em*em && area <= maxColorPixels
}

// bounds is where the glyph's layers have ink, from its origin.
func (cg colorGlyph) bounds() image.Rectangle {
	var b image.Rectangle
	cg.each(func(_ colorLayer, _ *image.Alpha, at image.Rectangle) { b = b.Union(at) })
	return b
}

// draw draws the glyph at dot into dst, each layer in its color (src for the text's).
func (cg colorGlyph) draw(dst draw.Image, src image.Image, dot fixed.Point26_6) {
	o := image.Pt(dot.X.Round(), dot.Y.Round())
	cg.each(func(l colorLayer, m *image.Alpha, at image.Rectangle) {
		draw.DrawMask(dst, at.Add(o), cg.color.ink(l.color, src), image.Point{}, m, image.Point{}, draw.Over)
	})
}

// tone is the glyph as one tint: a mask of its layers' lightness laid over one another, and where it
// goes from the glyph's origin. Kept, as glyphMask's masks are.
func (cg colorGlyph) tone() (*image.Alpha, image.Point) {
	k := maskKey{cg.font, cg.glyph, cg.ppem}
	tones.mu.Lock()
	if m, ok := tones.m[k]; ok {
		tones.mu.Unlock()
		return m.mask, m.at
	}
	tones.mu.Unlock()

	b := cg.bounds()
	light := make([]float32, b.Dx()*b.Dy())
	cg.each(func(l colorLayer, m *image.Alpha, at image.Rectangle) {
		v, opacity := cg.color.lightness(l.color)
		for y := at.Min.Y; y < at.Max.Y; y++ {
			row := m.Pix[(y-at.Min.Y)*m.Stride:]
			out := light[(y-b.Min.Y)*b.Dx():]
			for x := at.Min.X; x < at.Max.X; x++ {
				cover := float32(row[x-at.Min.X]) / 255 * opacity
				p := &out[x-b.Min.X]
				*p = *p*(1-cover) + v*cover
			}
		}
	})
	mask := image.NewAlpha(image.Rect(0, 0, b.Dx(), b.Dy()))
	for i, v := range light {
		mask.Pix[i] = uint8(v*255 + 0.5)
	}
	tones.mu.Lock()
	if len(tones.m) > 512 {
		clear(tones.m)
	}
	if tones.m == nil {
		tones.m = map[maskKey]maskEntry{}
	}
	tones.m[k] = maskEntry{mask, b.Min}
	tones.mu.Unlock()
	return mask, b.Min
}

var tones struct {
	mu sync.Mutex
	m  map[maskKey]maskEntry
}

// drawRun draws s, a run that needs no shaping, in face at dot with src, as font.Drawer's DrawString
// does, but with each color glyph drawn in its colors when inColor is set. It returns the dot after s.
func drawRun(dst draw.Image, src image.Image, face font.Face, dot fixed.Point26_6, s string, inColor bool) fixed.Point26_6 {
	st, ok := face.(*Stack)
	if !inColor || !ok || !st.colored {
		d := font.Drawer{Dst: dst, Src: src, Face: face, Dot: dot}
		d.DrawString(s)
		return d.Dot
	}
	prev := rune(-1)
	for _, c := range s {
		if prev >= 0 {
			dot.X += face.Kern(prev, c)
		}
		if cg, ok := st.colorOf(c); ok {
			cg.draw(dst, src, dot)
			dot.X += cg.advance
		} else {
			dr, mask, maskp, advance, _ := face.Glyph(dot, c)
			if !dr.Empty() {
				draw.DrawMask(dst, dr, src, image.Point{}, mask, maskp, draw.Over)
			}
			dot.X += advance
		}
		prev = c
	}
	return dot
}
