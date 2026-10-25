//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"
)

// faceTurn draws the whole turn page in the Face style (turn_face.go): the face on the left and the
// words beside it on the right, against the face, the block of them in the middle of the height.
func (r *renderer) faceTurn(s scene) {
	ground := turnGround(s)
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(ground), image.Point{}, draw.Src)
	top, bottom := r.s(headerH)+r.s(8), r.h-r.s(14)
	split := r.w * 42 / 100
	drawFace(r.dst, image.Rect(r.s(16), top, split, bottom), s.eq, s.now, ground)

	left := split + r.s(24)
	lines := r.eqWords(s, bottom-top, r.w-r.margin-left)
	words := 0
	for _, l := range lines {
		words += l.lineH
	}
	y := top + max(0, (bottom-top-words)/2)
	r.eqTextAt(lines, y, bottom, left)
}
