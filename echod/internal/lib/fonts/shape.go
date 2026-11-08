package fonts

// Complex-script shaping.
//
// A font.Face draws one glyph per character, which is all Latin, Greek, Cyrillic and the CJK scripts
// need. Khmer, Thai, Lao, Myanmar, Tibetan and the Indic scripts are not drawn that way: a cluster of
// characters becomes glyphs the font chooses (a Khmer consonant after the coeng sign U+17D2 becomes a
// subscript form under the one before it, a vowel sign moves to the left of its consonant, marks sit
// on top of their base) through the font's OpenType GSUB and GPOS tables. Drawn a character at a
// time, such text comes out as the right letters in the wrong shapes and places.
//
// DrawString, MeasureString and BoundString stand in for font.Drawer's DrawString, font.MeasureString
// and font.BoundString. Text in a Stack whose complex-script characters are drawn by a font of one's
// own is split into runs; runs of a complex script are shaped by go-text/typesetting's port of
// HarfBuzz and drawn glyph by glyph from that font, and everything else is drawn exactly as before.
// With no such run (the usual case, and every face that is not a Stack) they are font.Drawer and
// font.BoundString themselves.

import (
	"fmt"
	"image"
	"image/draw"
	"io"
	"log/slog"
	"sync"
	"unicode"

	"github.com/go-text/typesetting/di"
	ttf "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// shapedScripts are the scripts drawn by shaping rather than a character at a time: those whose
// clusters reorder, stack or substitute, and run left to right (right-to-left scripts need the
// bidirectional algorithm first, which this does not do).
var shapedScripts = map[language.Script]bool{
	language.Khmer:       true,
	language.Thai:        true,
	language.Lao:         true,
	language.Myanmar:     true,
	language.Tibetan:     true,
	language.Devanagari:  true,
	language.Bengali:     true,
	language.Gurmukhi:    true,
	language.Gujarati:    true,
	language.Oriya:       true,
	language.Tamil:       true,
	language.Telugu:      true,
	language.Kannada:     true,
	language.Malayalam:   true,
	language.Sinhala:     true,
	language.Balinese:    true,
	language.Javanese:    true,
	language.Tai_Tham:    true,
	language.New_Tai_Lue: true,
}

// shapeSource is a font of one's own as go-text reads it, opened the first time a run needs it.
type shapeSource struct {
	once sync.Once
	open func() (*ttf.Face, error)
	face *ttf.Face
}

func (s *shapeSource) get() *ttf.Face {
	s.once.Do(func() {
		f, err := s.open()
		if err != nil {
			slog.Warn("fonts: a font of one's own cannot be read for shaping; its scripts are drawn unshaped", "err", err)
			return
		}
		s.face = f
	})
	return s.face
}

var shapers struct {
	mu sync.Mutex
	m  map[*sfnt.Font]*shapeSource
}

// shaperFor is the shapeSource of an own font, one per font however many faces are made from it.
func shaperFor(e ownEntry) *shapeSource {
	shapers.mu.Lock()
	defer shapers.mu.Unlock()
	if s, ok := shapers.m[e.font]; ok {
		return s
	}
	if shapers.m == nil {
		shapers.m = map[*sfnt.Font]*shapeSource{}
	}
	fh, size := e.fh, e.size
	s := &shapeSource{open: func() (f *ttf.Face, err error) {
		defer func() {
			if p := recover(); p != nil {
				f, err = nil, errPanic(p)
			}
		}()
		// Its own reader: sfnt reads the same file with ReadAt, which a Seek here must not disturb.
		r := io.NewSectionReader(fh, 0, size)
		if collection(r) {
			fs, err := ttf.ParseTTC(r)
			if err != nil || len(fs) == 0 {
				return nil, err
			}
			return fs[0], nil // the first font, as own.go takes from a collection
		}
		return ttf.ParseTTF(r)
	}}
	shapers.m[e.font] = s
	return s
}

// collection is whether r is a font collection (.ttc), going back to its start either way.
func collection(r io.ReadSeeker) bool {
	var tag [4]byte
	_, err := io.ReadFull(r, tag[:])
	r.Seek(0, io.SeekStart)
	return err == nil && string(tag[:]) == "ttcf"
}

type panicError struct{ v any }

func (p panicError) Error() string { return "panic: " + fmt.Sprint(p.v) }
func errPanic(v any) error         { return panicError{v} }

// run is a stretch of text drawn one way: shaped from a font of one's own, or as before.
type run struct {
	text  string
	shape *shaped // nil for a run drawn as before
}

// shaped is a run as the shaper laid it out, with the font its glyphs are drawn from.
type shaped struct {
	glyphs  []shaping.Glyph
	advance fixed.Int26_6
	font    *sfnt.Font
	ppem    fixed.Int26_6
}

// complexRune is whether r belongs to a shaped script, and mark whether it is a mark or joiner that
// goes with the run before it whatever its own script.
func complexRune(r rune) (complex, mark bool) {
	if r == 0x200C || r == 0x200D || unicode.In(r, unicode.Mn, unicode.Mc, unicode.Me) {
		mark = true
	}
	return shapedScripts[language.LookupScript(r)], mark
}

// needsShaping is whether s has a character of a shaped script at all: the quick test that keeps
// every other string on the old path.
func needsShaping(s string) bool {
	for _, r := range s {
		if r >= 0x0900 { // nothing below Devanagari is shaped here
			if c, _ := complexRune(r); c {
				return true
			}
		}
	}
	return false
}

// runs is s split for drawing in face, or nil when all of it is drawn as before.
func runs(face font.Face, s string) []run {
	st, ok := face.(*Stack)
	if !ok || st.size <= 0 || !needsShaping(s) {
		return nil
	}
	var out []run
	shapedAny := false
	start, inComplex := 0, false
	flush := func(end int) {
		if end <= start {
			return
		}
		text := s[start:end]
		r := run{text: text}
		if inComplex {
			r.shape = st.shape(text)
			shapedAny = shapedAny || r.shape != nil
		}
		if n := len(out); n > 0 && out[n-1].shape == nil && r.shape == nil {
			out[n-1].text += text // two unshaped runs side by side are one
		} else {
			out = append(out, r)
		}
		start = end
	}
	for i, r := range s {
		c, mark := complexRune(r)
		if mark && inComplex {
			continue // a mark or joiner stays with the cluster it follows
		}
		if c != inComplex {
			flush(i)
			inComplex = c
		}
	}
	flush(len(s))
	if !shapedAny {
		return nil
	}
	return out
}

// shapeCache keeps shaped runs: the same few lines are drawn every second.
var shapeCache struct {
	mu     sync.Mutex
	m      map[shapeKey]*shaped
	shaper shaping.HarfbuzzShaper
}

type shapeKey struct {
	face *ttf.Face
	size float64
	text string
}

// shape lays out text, all of one shaped script, in the layer that draws its first character, or is
// nil when that layer is not a font of one's own that go-text can read.
func (s *Stack) shape(text string) (out *shaped) {
	first := []rune(text)[0]
	s.mu.Lock()
	i := s.pick(first)
	var l Layer
	if i >= 0 {
		l = s.layers[i]
	}
	drawn := i >= 0 && s.has(l, first)
	s.mu.Unlock()
	if !drawn || l.shaper == nil || l.Font == nil {
		return nil
	}
	gt := l.shaper.get()
	if gt == nil {
		return nil
	}
	defer func() {
		if p := recover(); p != nil {
			slog.Error("fonts: shaping panicked; the text is drawn unshaped", "err", fmt.Sprint(p))
			out = nil
		}
	}()
	k := shapeKey{gt, s.size, text}
	shapeCache.mu.Lock()
	defer shapeCache.mu.Unlock()
	if sh, ok := shapeCache.m[k]; ok {
		return sh
	}
	runes := []rune(text)
	ppem := fixed.Int26_6(s.size * 64)
	o := shapeCache.shaper.Shape(shaping.Input{
		Text:      runes,
		RunStart:  0,
		RunEnd:    len(runes),
		Direction: di.DirectionLTR,
		Face:      gt,
		Size:      ppem,
		Script:    language.LookupScript(first),
	})
	sh := &shaped{glyphs: o.Glyphs, advance: o.Advance, font: l.Font, ppem: ppem}
	if len(shapeCache.m) > 256 {
		clear(shapeCache.m)
	}
	if shapeCache.m == nil {
		shapeCache.m = map[shapeKey]*shaped{}
	}
	shapeCache.m[k] = sh
	return sh
}

// glyphMask is glyph g of f at ppem as a coverage mask, and where the mask's top left is from the
// glyph's origin; nil for a glyph with no outline (a space, a zero-width joiner).
func glyphMask(f *sfnt.Font, g sfnt.GlyphIndex, ppem fixed.Int26_6) (*image.Alpha, image.Point) {
	k := maskKey{f, g, ppem}
	masks.mu.Lock()
	if m, ok := masks.m[k]; ok {
		masks.mu.Unlock()
		return m.mask, m.at
	}
	masks.mu.Unlock()

	m := rasterize(f, g, ppem)
	masks.mu.Lock()
	if len(masks.m) > 2048 {
		clear(masks.m)
	}
	if masks.m == nil {
		masks.m = map[maskKey]maskEntry{}
	}
	masks.m[k] = m
	masks.mu.Unlock()
	return m.mask, m.at
}

// rasterize draws glyph g of f at ppem into a mask. A font of one's own that panics here, past the
// recover around its parsing and shaping, leaves that glyph out rather than taking the daemon down.
func rasterize(f *sfnt.Font, g sfnt.GlyphIndex, ppem fixed.Int26_6) (m maskEntry) {
	defer func() {
		if p := recover(); p != nil {
			slog.Warn("a font of one's own could not draw a glyph", "glyph", g, "err", p)
			m = maskEntry{}
		}
	}()
	var buf sfnt.Buffer
	segs, err := f.LoadGlyph(&buf, g, ppem, nil)
	if err == nil && len(segs) > 0 {
		b := segs.Bounds()
		r := image.Rect(b.Min.X.Floor(), b.Min.Y.Floor(), b.Max.X.Ceil(), b.Max.Y.Ceil())
		if !r.Empty() {
			z := vector.NewRasterizer(r.Dx(), r.Dy())
			pt := func(p fixed.Point26_6) (float32, float32) {
				return float32(p.X)/64 - float32(r.Min.X), float32(p.Y)/64 - float32(r.Min.Y)
			}
			for _, s := range segs {
				switch s.Op {
				case sfnt.SegmentOpMoveTo:
					z.MoveTo(pt(s.Args[0]))
				case sfnt.SegmentOpLineTo:
					z.LineTo(pt(s.Args[0]))
				case sfnt.SegmentOpQuadTo:
					x1, y1 := pt(s.Args[0])
					x2, y2 := pt(s.Args[1])
					z.QuadTo(x1, y1, x2, y2)
				case sfnt.SegmentOpCubeTo:
					x1, y1 := pt(s.Args[0])
					x2, y2 := pt(s.Args[1])
					x3, y3 := pt(s.Args[2])
					z.CubeTo(x1, y1, x2, y2, x3, y3)
				}
			}
			mask := image.NewAlpha(image.Rect(0, 0, r.Dx(), r.Dy()))
			z.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})
			m = maskEntry{mask, r.Min}
		}
	}
	return m
}

type maskKey struct {
	f    *sfnt.Font
	g    sfnt.GlyphIndex
	ppem fixed.Int26_6
}

type maskEntry struct {
	mask *image.Alpha
	at   image.Point
}

var masks struct {
	mu sync.Mutex
	m  map[maskKey]maskEntry
}

// each calls fn for each glyph of sh with its mask's place in dst, drawn with the pen at dot. The
// shaper's offsets are y-up and the masks' y-down.
func (sh *shaped) each(dot fixed.Point26_6, fn func(mask *image.Alpha, r image.Rectangle)) {
	pen := dot
	for _, g := range sh.glyphs {
		m, at := glyphMask(sh.font, sfnt.GlyphIndex(g.GlyphID), sh.ppem)
		if m != nil {
			x := (pen.X + g.XOffset).Round() + at.X
			y := (pen.Y - g.YOffset).Round() + at.Y
			fn(m, m.Bounds().Add(image.Pt(x, y)))
		}
		pen.X += g.XAdvance
		pen.Y -= g.YAdvance
	}
}

// DrawString draws s in face at dot with src, as font.Drawer's DrawString does, shaping the runs of
// a complex script a font of one's own draws. It returns the dot after s.
func DrawString(dst draw.Image, src image.Image, face font.Face, dot fixed.Point26_6, s string) fixed.Point26_6 {
	rs := runs(face, s)
	if rs == nil {
		d := font.Drawer{Dst: dst, Src: src, Face: face, Dot: dot}
		d.DrawString(s)
		return d.Dot
	}
	for _, r := range rs {
		if r.shape == nil {
			d := font.Drawer{Dst: dst, Src: src, Face: face, Dot: dot}
			d.DrawString(r.text)
			dot = d.Dot
			continue
		}
		r.shape.each(dot, func(m *image.Alpha, at image.Rectangle) {
			draw.DrawMask(dst, at, src, at.Min, m, image.Point{}, draw.Over)
		})
		dot.X += r.shape.advance
	}
	return dot
}

// MeasureString is how far s advances in face, as font.MeasureString, with complex-script runs
// shaped.
func MeasureString(face font.Face, s string) fixed.Int26_6 {
	rs := runs(face, s)
	if rs == nil {
		return font.MeasureString(face, s)
	}
	var adv fixed.Int26_6
	for _, r := range rs {
		if r.shape == nil {
			adv += font.MeasureString(face, r.text)
		} else {
			adv += r.shape.advance
		}
	}
	return adv
}

// BoundString is the bounds of s drawn in face from the origin, and its advance, as
// font.BoundString, with complex-script runs shaped.
func BoundString(face font.Face, s string) (bounds fixed.Rectangle26_6, advance fixed.Int26_6) {
	rs := runs(face, s)
	if rs == nil {
		return font.BoundString(face, s)
	}
	var dot fixed.Point26_6
	union := func(b fixed.Rectangle26_6) {
		if b.Empty() {
			return
		}
		if bounds.Empty() {
			bounds = b
		} else {
			bounds = bounds.Union(b)
		}
	}
	for _, r := range rs {
		if r.shape == nil {
			b, adv := font.BoundString(face, r.text)
			union(b.Add(dot))
			dot.X += adv
			continue
		}
		r.shape.each(dot, func(_ *image.Alpha, at image.Rectangle) {
			union(fixed.R(at.Min.X, at.Min.Y, at.Max.X, at.Max.Y))
		})
		dot.X += r.shape.advance
	}
	return bounds, dot.X
}
