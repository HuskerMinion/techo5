//go:build !dot && !spot && !panel

package camera

import "testing"

// A frame as MIPI packs it comes out in the Show 5's packing with the same pixels, less the columns
// dropped at the start of each line, whatever the line's padding holds.
func TestRepackCAMSSKeepsThePixels(t *testing.T) {
	const srcW, w, h, line = 24, 16, 3, 32 // 24 pixels are 30 bytes, padded to 32
	px := func(x, y int) uint16 { return uint16((x*37 + y*101 + 5) % 1024) }
	raw := make([]byte, line*h)
	for i := range raw {
		raw[i] = 0xa5 // the padding is never read as a pixel
	}
	for y := 0; y < h; y++ {
		for x := 0; x < srcW; x += 4 {
			g := raw[y*line+x/4*5:]
			var lo byte
			for k := 0; k < 4; k++ {
				v := px(x+k, y)
				g[k] = byte(v >> 2)
				lo |= byte(v&3) << (2 * k)
			}
			g[4] = lo
		}
	}
	for skip := 0; skip < 4; skip++ {
		out := make([]byte, w*10/8*h)
		repackCAMSS(raw, out, line, skip, w, h)
		got := make([]uint16, w)
		for y := 0; y < h; y++ {
			unpackLine(out[y*w*10/8:(y+1)*w*10/8], got)
			for x := range got {
				if want := px(x+skip, y); got[x] != want {
					t.Fatalf("skip %d: pixel (%d,%d) = %d, want %d", skip, x, y, got[x], want)
				}
			}
		}
	}
}

// The ThinkSmart View's sensor: V4L2, blue first once a column is dropped, the live view half the
// frame, and a frame that fits inside what the sensor hands over.
func TestThinkSmartSensor(t *testing.T) {
	s := thinkSmartSensor
	if !s.v4l2 || !s.blueFirst {
		t.Errorf("%+v: V4L2 and BGGR", s)
	}
	if s.outW*2 != s.w || s.outH*2 != s.h || s.w%4 != 0 || s.w*10%8 != 0 {
		t.Errorf("%+v: a half-size live view, and whole groups of four pixels a line", s)
	}
	if camssSkip%2 != 1 || s.w+camssSkip > camssW || s.h != camssH || (s.w/4+1)*5+5 > camssLine {
		t.Errorf("%+v: an odd number of columns dropped, inside the sensor's %dx%d and its %d-byte line", s, camssW, camssH, camssLine)
	}
	if s.maxShut > s.frameLines || s.blackCap < 2*s.pedestal {
		t.Errorf("%+v: the shutter stays in a frame, and black covers the pedestal", s)
	}
}
