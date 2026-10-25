//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"

	xdraw "golang.org/x/image/draw"
)

// screenSized keeps a picture from home scaled to the screen. home makes photos, the now-playing
// picture and the rain map at the screen's own size, and the pages draw them one to one; but on a
// TECHO5 Panel one made before the screen said its size (home.SetPanelSize) is another size until the
// next one comes, and drawn one to one it would fill only a corner. The frame loop alone uses it.
type screenSized struct {
	src, out *image.RGBA
}

// of is img as a w by h picture: img itself when it is that size already, which is every picture on
// every device but that one; otherwise scaled to cover it, and kept, so a picture that stays up for
// minutes is scaled once.
func (k *screenSized) of(img *image.RGBA, w, h int) *image.RGBA {
	if img == nil || (img.Rect.Dx() == w && img.Rect.Dy() == h) {
		return img
	}
	if k.src == img && k.out.Rect.Dx() == w && k.out.Rect.Dy() == h {
		return k.out
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(out, out.Rect, img, cover(img.Rect, out.Rect), draw.Src, nil)
	k.src, k.out = img, out
	return out
}
