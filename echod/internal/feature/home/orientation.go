package home

import (
	"encoding/binary"
	"image"
	"image/draw"
)

// Cameras and phones save a photo the way the sensor read it and note in its EXIF data how to turn
// it for viewing. Without reading that note a portrait photo comes out lying on its side.

// exifOrientation is a JPEG's EXIF orientation, 1 to 8, or 1 (as it is) when it has none.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		if marker == 0xDA || marker == 0xD9 { // image data or end: no EXIF before it
			return 1
		}
		size := int(binary.BigEndian.Uint16(b[i+2:]))
		seg := b[i+4 : min(i+2+size, len(b))]
		if marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

// tiffOrientation reads tag 0x0112 from a TIFF header's first directory.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int(order.Uint32(t[4:]))
	if ifd+2 > len(t) {
		return 1
	}
	n := int(order.Uint16(t[ifd:]))
	for e := 0; e < n; e++ {
		at := ifd + 2 + e*12
		if at+12 > len(t) {
			return 1
		}
		if order.Uint16(t[at:]) == 0x0112 {
			if v := int(order.Uint16(t[at+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// upright turns and mirrors src as an EXIF orientation says, so it reads the right way up.
func upright(src image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	in := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(in, in.Rect, src, b.Min, draw.Src)

	// Where the pixel at (x, y) of the stored image lands in the upright one.
	var ow, oh int
	var to func(x, y int) (int, int)
	switch orientation {
	case 2: // mirrored
		ow, oh, to = w, h, func(x, y int) (int, int) { return w - 1 - x, y }
	case 3: // upside down
		ow, oh, to = w, h, func(x, y int) (int, int) { return w - 1 - x, h - 1 - y }
	case 4: // upside down, mirrored
		ow, oh, to = w, h, func(x, y int) (int, int) { return x, h - 1 - y }
	case 5: // on its side, mirrored
		ow, oh, to = h, w, func(x, y int) (int, int) { return y, x }
	case 6: // turned a quarter anticlockwise: turn it clockwise
		ow, oh, to = h, w, func(x, y int) (int, int) { return h - 1 - y, x }
	case 7: // on its side the other way, mirrored
		ow, oh, to = h, w, func(x, y int) (int, int) { return h - 1 - y, w - 1 - x }
	case 8: // turned a quarter clockwise: turn it anticlockwise
		ow, oh, to = h, w, func(x, y int) (int, int) { return y, w - 1 - x }
	}
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for y := range h {
		for x := range w {
			nx, ny := to(x, y)
			si, di := in.PixOffset(x, y), out.PixOffset(nx, ny)
			copy(out.Pix[di:di+4], in.Pix[si:si+4])
		}
	}
	return out
}
