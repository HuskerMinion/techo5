package fonts

import (
	"fmt"
	"image"
	"log/slog"
	"slices"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Layer is one face in a Stack, with the font it was made from, which says which characters it has
// a glyph for. A layer with no Font is taken to have every character: the last resort.
type Layer struct {
	Face font.Face
	Font *sfnt.Font

	// Broke, when set, is told when the layer panicked and was dropped (a font file of one's own
	// broken in a way the checks before use did not find), with what the panic said.
	Broke func(why string)
	// shaper, when set, shapes runs of a complex script (Khmer, Thai, the Indic scripts...) in this
	// layer's font: a font of one's own (shape.go).
	shaper *shapeSource
}

// Stack is a face drawn from several: each character from the first layer that has a glyph for it.
// The chosen font comes first and Go after it, so a character the chosen font lacks ("♪", say) is
// still drawn rather than left blank or boxed; another script's font can go in between the same way.
// A character no layer has is the first layer's to draw, as it would be on its own.
//
// Line metrics (ascent, descent, height) are the metrics face's when there is one, else the first
// layer's: a page laid out for one font's line spacing keeps it whichever font draws the letters.
//
// A layer that panics is dropped from the stack, and the character drawn from the next one that has
// it, so a broken font file cannot take the daemon down.
//
// Kerning applies only between two characters drawn from the same layer. Like any font.Face, a Stack
// is for one goroutine at a time to draw with: the mask Glyph returns is the layer's own and changes
// with the next Glyph call. Its lock only keeps its own state (the layers it still has, the buffer
// it looks glyphs up with) whole.
type Stack struct {
	mu      sync.Mutex
	buf     sfnt.Buffer
	layers  []Layer
	metrics font.Face
	// size is the pixel size the layers were made at, 0 when not known: what complex-script runs
	// are shaped at (shape.go).
	size float64
}

// NewStack makes a Stack of layers, in the order they are tried, with metrics' line metrics (nil for
// the first layer's).
func NewStack(metrics font.Face, layers ...Layer) *Stack {
	return &Stack{layers: layers, metrics: metrics}
}

// guard runs fn, a call into layer i. Should it panic, the layer is dropped, its Broke told, and
// guard is false. Called under s.mu.
func (s *Stack) guard(i int, fn func()) (ok bool) {
	defer func() {
		if p := recover(); p != nil {
			l := s.layers[i]
			s.layers = slices.Delete(slices.Clone(s.layers), i, i+1)
			why := fmt.Sprint(p)
			slog.Error("fonts: a font panicked while drawing; it is dropped", "err", why)
			if l.Broke != nil {
				l.Broke(why)
			}
			ok = false
		}
	}()
	fn()
	return true
}

// pick is the place of the layer that draws r, -1 with none left. Called under s.mu.
func (s *Stack) pick(r rune) int {
	for i := 0; i < len(s.layers); {
		var has bool
		if !s.guard(i, func() { has = s.has(s.layers[i], r) }) {
			continue // dropped: the next layer is at i now
		}
		if has {
			return i
		}
		i++
	}
	if len(s.layers) == 0 {
		return -1
	}
	return 0
}

// with runs fn on the face of the layer that draws r, trying the next should that layer panic, and
// is false when no layer is left. Called under s.mu.
func (s *Stack) with(r rune, fn func(font.Face)) bool {
	for {
		i := s.pick(r)
		if i < 0 {
			return false
		}
		if s.guard(i, func() { fn(s.layers[i].Face) }) {
			return true
		}
	}
}

// has is whether l has a glyph for r. Called under s.mu.
func (s *Stack) has(l Layer, r rune) bool {
	if l.Font == nil {
		return true
	}
	i, err := glyphIndex(l.Font, &s.buf, r)
	return err == nil && i != 0
}

// glyphIndex is sfnt's GlyphIndex, a variable so a test can have a font break for one character.
var glyphIndex = (*sfnt.Font).GlyphIndex

// Has is whether any layer has a glyph for r: whether the stack draws it as itself.
func (s *Stack) Has(r rune) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.pick(r)
	return i >= 0 && s.has(s.layers[i], r)
}

func (s *Stack) Glyph(dot fixed.Point26_6, r rune) (dr image.Rectangle, mask image.Image, maskp image.Point, advance fixed.Int26_6, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.with(r, func(f font.Face) { dr, mask, maskp, advance, ok = f.Glyph(dot, r) })
	return
}

func (s *Stack) GlyphBounds(r rune) (bounds fixed.Rectangle26_6, advance fixed.Int26_6, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.with(r, func(f font.Face) { bounds, advance, ok = f.GlyphBounds(r) })
	return
}

func (s *Stack) GlyphAdvance(r rune) (advance fixed.Int26_6, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.with(r, func(f font.Face) { advance, ok = f.GlyphAdvance(r) })
	return
}

func (s *Stack) Kern(r0, r1 rune) (k fixed.Int26_6) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Picked again until no layer is dropped on the way: a layer dropped while r1's is found shifts
	// the places after it, and r0's place would then be another layer's.
	var i0, i1 int
	for n := -1; n != len(s.layers); {
		n = len(s.layers)
		i0, i1 = s.pick(r0), s.pick(r1)
	}
	if i0 < 0 || i0 != i1 {
		return 0
	}
	s.guard(i0, func() { k = s.layers[i0].Face.Kern(r0, r1) })
	return k
}

func (s *Stack) Metrics() (m font.Metrics) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metrics != nil {
		return s.metrics.Metrics()
	}
	if len(s.layers) == 0 {
		return font.Metrics{}
	}
	s.guard(0, func() { m = s.layers[0].Face.Metrics() })
	return m
}

// Close closes every layer's face and the metrics face, each once.
func (s *Stack) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	shared := false
	for _, l := range s.layers {
		shared = shared || l.Face == s.metrics
		if err := l.Face.Close(); err != nil && first == nil {
			first = err
		}
	}
	if s.metrics != nil && !shared {
		if err := s.metrics.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
