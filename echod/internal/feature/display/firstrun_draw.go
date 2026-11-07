//go:build !dot

package display

import (
	"image"
	"image/color"
	"image/draw"

	"rsc.io/qr"
)

// splitToFit cuts s into the fewest equal pieces that each fit.
func splitToFit(s string, fits func(string) bool) []string {
	for n := 1; n <= len(s); n++ {
		size := (len(s) + n - 1) / n
		var parts []string
		ok := true
		for i := 0; i < len(s); i += size {
			p := s[i:min(len(s), i+size)]
			parts = append(parts, p)
			ok = ok && fits(p)
		}
		if ok {
			return parts
		}
	}
	return []string{s}
}

// qrCode draws text as a QR code in box, black on white with the four-module quiet zone around it that
// a phone's camera needs, as large as fits in whole pixels per module.
func (r *paint) qrCode(text string, box image.Rectangle) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return
	}
	n := c.Size + 8
	px := min(box.Dx(), box.Dy()) / n
	if px < 1 {
		return
	}
	at := image.Rect(box.Min.X, box.Min.Y, box.Min.X+px*n, box.Min.Y+px*n)
	draw.Draw(r.dst, at, image.NewUniform(color.White), image.Point{}, draw.Src)
	black := image.NewUniform(color.Black)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Black(x, y) {
				m := image.Rect(at.Min.X+(x+4)*px, at.Min.Y+(y+4)*px, at.Min.X+(x+5)*px, at.Min.Y+(y+5)*px)
				draw.Draw(r.dst, m, black, image.Point{}, draw.Src)
			}
		}
	}
}
