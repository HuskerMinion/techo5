package fonts

import (
	"image"
	"sync/atomic"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// No font the screen offers has emoji (#639). The first time the screen is asked to draw one that no
// font in use has, emojiMissed is set, and feature/cjkfont fetches an emoji font into the fonts folder
// (own.go); from then on the stack draws emoji from it, a character at a time: in color from the color
// emoji font that comes with it (color.go), and in black and white from Noto Emoji where that has none.

// emojiMissed is whether an emoji was drawn that no font had.
var emojiMissed atomic.Bool

// EmojiMissed is whether the screen has had an emoji to draw that no font in use has a glyph for.
func EmojiMissed() bool { return emojiMissed.Load() }

// emoji is whether r is in the blocks emoji are in: the pictographs, emoticons, transport and the
// rest from U+1F000 on, and the older symbols and dingbats (☀, ✅).
func emoji(r rune) bool {
	return r >= 0x1F000 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF
}

// skinTone is whether r is one of the five skin tone modifiers. Drawn a character at a time it would
// be a swatch after the hand it colors, so it is drawn as nothing.
func skinTone(r rune) bool { return r >= 0x1F3FB && r <= 0x1F3FF }

// nothing is the glyph a skin tone is drawn as: no width, no ink.
var nothing = image.NewAlpha(image.Rectangle{})

// watched is the face Face gives when Go is the only font, so an emoji drawn in it is noticed there
// too; it draws as Go does otherwise.
type watched struct {
	font.Face
	font *sfnt.Font
}

// missed notes r as missed when it is an emoji f has no glyph for.
func missed(f *sfnt.Font, r rune) {
	if !emoji(r) || emojiMissed.Load() {
		return
	}
	var buf sfnt.Buffer
	if i, err := f.GlyphIndex(&buf, r); err != nil || i == 0 {
		emojiMissed.Store(true)
	}
}

func (w watched) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	if skinTone(r) {
		return image.Rectangle{}, nothing, image.Point{}, 0, true
	}
	missed(w.font, r)
	return w.Face.Glyph(dot, r)
}

func (w watched) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	if skinTone(r) {
		return fixed.Rectangle26_6{}, 0, true
	}
	missed(w.font, r)
	return w.Face.GlyphBounds(r)
}

func (w watched) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	if skinTone(r) {
		return 0, true
	}
	missed(w.font, r)
	return w.Face.GlyphAdvance(r)
}
