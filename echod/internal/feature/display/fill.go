package display

import (
	"image"
	"image/color"

	"github.com/HuskerMinion/techo5/echod/internal/lib/memx"
)

// fillCanvas paints the whole of dst c, as draw.Draw with a uniform source and draw.Src does, a word at
// a time: image/draw fills one row and copies it down, which on a Tegra 3 (the Surface RT) takes a full
// canvas 10 ms and this 3 (lib/memx).
func fillCanvas(dst *image.RGBA, c color.Color) {
	p := color.RGBAModel.Convert(c).(color.RGBA)
	v := uint32(p.R) | uint32(p.G)<<8 | uint32(p.B)<<16 | uint32(p.A)<<24
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	if w <= 0 || h <= 0 {
		return
	}
	if dst.Stride == 4*w {
		memx.Fill(dst.Pix[:4*w*h], v)
		return
	}
	for y := range h {
		memx.Fill(dst.Pix[y*dst.Stride:y*dst.Stride+4*w], v)
	}
}
