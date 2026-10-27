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

// FullYUYV is Full's picture in YUYV, whether Full has developed the frame already or not.
func TestFullYUYVIsFullsPicture(t *testing.T) {
	g := streamed()
	raw := make([]byte, g.bpl*g.h)
	rand.New(rand.NewSource(11)).Read(raw)
	img := (&Frame{raw: raw}).Full()
	want := make([]byte, g.w*g.h*2)
	for y := range g.h {
		rgbaToYUYV(want[y*g.w*2:(y+1)*g.w*2], img.Pix[y*img.Stride:])
	}
	fresh := make([]byte, len(want))
	if !(&Frame{raw: raw}).FullYUYV(fresh) || !bytes.Equal(fresh, want) {
		t.Error("FullYUYV of a frame not developed yet is not Full's picture")
	}
	developed := &Frame{raw: raw}
	developed.Full()
	again := make([]byte, len(want))
	if !developed.FullYUYV(again) || !bytes.Equal(again, want) {
		t.Error("FullYUYV of a frame Full developed is not its picture")
	}
	if (&Frame{raw: raw}).FullYUYV(make([]byte, len(want)-1)) {
		t.Error("FullYUYV took a buffer too small")
	}
}

// YUYV of known colors: white, black, red and blue, full-range BT.601.
func TestRGBAToYUYV(t *testing.T) {
	out := make([]byte, 8)
	rgbaToYUYV(out, []uint8{255, 255, 255, 255, 0, 0, 0, 255, 255, 0, 0, 255, 0, 0, 255, 255})
	// The pairs are white with black, red with blue: luma each, chroma their average.
	want := []byte{255, 128, 0, 128, 76, 170, 29, 181}
	for i := range want {
		if d := int(out[i]) - int(want[i]); d < -1 || d > 1 {
			t.Fatalf("YUYV %v, want %v", out, want)
		}
	}
}
