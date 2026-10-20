//go:build !dot && !spot

package camera

import (
	"bytes"
	"testing"
)

// i420Reference is the develop as first written, a cell at a time from unpacked lines: what
// i420Rows must match exactly.
func i420Reference(raw []byte, bpl, w, h int, dst []byte, lut *[3][2048]uint8, blueFirst bool) {
	yp, cb, cr := dst[:w*h], dst[w*h:w*h+w*h/4], dst[w*h+w*h/4:w*h*3/2]
	row0, row1 := make([]uint16, w), make([]uint16, w)
	for cy := 0; cy < h/2; cy++ {
		unpackLine(raw[(2*cy)*bpl:(2*cy+1)*bpl], row0)
		unpackLine(raw[(2*cy+1)*bpl:(2*cy+2)*bpl], row1)
		y0, y1 := yp[(2*cy)*w:], yp[(2*cy+1)*w:]
		co := cy * (w / 2)
		for cx := 0; cx < w/2; cx++ {
			r, g0, g1, bl := row0[2*cx], row0[2*cx+1], row1[2*cx], row1[2*cx+1]
			if blueFirst {
				r, bl = bl, r
			}
			R, B := int(lut[0][r<<1]), int(lut[2][bl<<1])
			G := int(lut[1][g0+g1])
			rb := 66*R + 25*B + 128
			yc := uint8((rb+129*G)>>8 + 16)
			ya := uint8((rb+129*int(lut[1][g0<<1]))>>8 + 16)
			yb := uint8((rb+129*int(lut[1][g1<<1]))>>8 + 16)
			y0[2*cx], y0[2*cx+1], y1[2*cx], y1[2*cx+1] = yc, ya, yb, yc
			cb[co+cx] = uint8((-38*R-74*G+112*B+128)>>8 + 128)
			cr[co+cx] = uint8((112*R-94*G-18*B+128)>>8 + 128)
		}
	}
}

func TestI420MatchesReference(t *testing.T) {
	f := &Frame{raw: benchRaw}
	lut := f.level().tables()
	w, h := sensorW, sensorH
	for _, blueFirst := range []bool{false, true} {
		want, got := make([]byte, w*h*3/2), make([]byte, w*h*3/2)
		i420Reference(benchRaw, bytesPerLine, w, h, want, lut, blueFirst)
		yp, cb, cr := got[:w*h], got[w*h:w*h+w*h/4], got[w*h+w*h/4:]
		i420Rows(benchRaw, bytesPerLine, w, 0, h/4, yp, cb, cr, lut, blueFirst) // in two bands, as I420 splits it
		i420Rows(benchRaw, bytesPerLine, w, h/4, h/2, yp, cb, cr, lut, blueFirst)
		if !bytes.Equal(want, got) {
			for i := range want {
				if want[i] != got[i] {
					t.Fatalf("blueFirst %v: byte %d is %d, want %d", blueFirst, i, got[i], want[i])
				}
			}
		}
	}
}

// sampleIn reads an even step's cells straight from the packed lines: the same tone as from lines
// unpacked whole (an odd step still unpacks them).
func TestSampleInMatchesUnpacked(t *testing.T) {
	geo := streamed()
	for _, step := range []int{2, 4} {
		got := sampleIn(benchRaw, step, geo)
		want := sampleInUnpacking(benchRaw, step, geo)
		if got != want {
			t.Fatalf("step %d: %+v, want %+v", step, got, want)
		}
	}
}
