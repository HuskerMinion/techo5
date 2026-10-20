package display

import (
	"image"
	"image/color"
	"image/draw"
)

// washOver draws c over r of dst, exactly as draw.Draw with an image.Uniform and draw.Over does, but
// through four 256-entry tables: for a solid color each byte's result depends on that byte alone, so
// the per-pixel multiply and divide by 0xffff (image/draw's drawFillOver) are done 256 times per
// channel instead of once per pixel. A wash over the whole panel goes from being the costliest thing
// the screen draws on a small core to a pass of table lookups.
func washOver(dst *image.RGBA, r image.Rectangle, c color.Color) {
	r = r.Intersect(dst.Rect)
	if r.Empty() {
		return
	}
	sr, sg, sb, sa := c.RGBA()
	if sa == 0 {
		return
	}
	if sa == 0xffff {
		draw.Draw(dst, r, image.NewUniform(c), image.Point{}, draw.Src)
		return
	}
	const m = 1<<16 - 1
	a := (m - sa) * 0x101
	var lut [4][256]uint8
	for v := uint32(0); v < 256; v++ {
		lut[0][v] = uint8((v*a/m + sr) >> 8)
		lut[1][v] = uint8((v*a/m + sg) >> 8)
		lut[2][v] = uint8((v*a/m + sb) >> 8)
		lut[3][v] = uint8((v*a/m + sa) >> 8)
	}
	i0 := dst.PixOffset(r.Min.X, r.Min.Y)
	n := r.Dx() * 4
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := dst.Pix[i0 : i0+n : i0+n]
		for i := 0; i+3 < len(row); i += 4 {
			row[i] = lut[0][row[i]]
			row[i+1] = lut[1][row[i+1]]
			row[i+2] = lut[2][row[i+2]]
			row[i+3] = lut[3][row[i+3]]
		}
		i0 += dst.Stride
	}
}
