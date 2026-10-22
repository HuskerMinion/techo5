//go:build !dot && !spot && !panel

package camera

import (
	"bytes"
	"image"
	"math/rand"
	"testing"
)

// fullReference is Full as it was written first, a pixel at a time, kept to hold the fast one to it.
func fullReference(f *Frame) *image.RGBA {
	g := streamed()
	w, h := g.w, g.h
	px := make([]uint16, w*h)
	for y := 0; y < h; y++ {
		unpackLine(f.raw[y*g.bpl:(y+1)*g.bpl], px[y*w:(y+1)*w])
	}
	at := func(x, y int) uint32 {
		if x < 0 {
			x = 1
		} else if x >= w {
			x = w - 2
		}
		if y < 0 {
			y = 1
		} else if y >= h {
			y = h - 2
		}
		return uint32(px[y*w+x])
	}
	luts := f.level().tables()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b uint32
			c := at(x, y)
			switch (y&1)<<1 | x&1 {
			case 0:
				r = c
				g = (at(x-1, y) + at(x+1, y) + at(x, y-1) + at(x, y+1)) / 4
				b = (at(x-1, y-1) + at(x+1, y-1) + at(x-1, y+1) + at(x+1, y+1)) / 4
			case 1:
				g = c
				r = (at(x-1, y) + at(x+1, y)) / 2
				b = (at(x, y-1) + at(x, y+1)) / 2
			case 2:
				g = c
				b = (at(x-1, y) + at(x+1, y)) / 2
				r = (at(x, y-1) + at(x, y+1)) / 2
			default:
				b = c
				g = (at(x-1, y) + at(x+1, y) + at(x, y-1) + at(x, y+1)) / 4
				r = (at(x-1, y-1) + at(x+1, y-1) + at(x-1, y+1) + at(x+1, y+1)) / 4
			}
			if sensor.blueFirst {
				r, b = b, r
			}
			j := (y*w + x) * 4
			img.Pix[j] = luts[0][r<<1]
			img.Pix[j+1] = luts[1][g<<1]
			img.Pix[j+2] = luts[2][b<<1]
			img.Pix[j+3] = 255
		}
	}
	return img
}

// The banded, two-a-step Full gives exactly the pixels the plain one did, for either Bayer order.
func TestFullMatchesTheReference(t *testing.T) {
	saved := sensor
	defer func() { sensor = saved }()
	for _, blue := range []bool{false, true} {
		sensor.blueFirst = blue
		g := streamed()
		raw := make([]byte, g.bpl*g.h)
		rand.New(rand.NewSource(7)).Read(raw)
		got := (&Frame{raw: raw}).Full()
		want := fullReference(&Frame{raw: raw})
		if !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("blueFirst=%v: the fast Full differs from the reference", blue)
		}
	}
}
