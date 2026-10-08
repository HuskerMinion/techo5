//go:build !dot && !spot

package camera

import "testing"

// A frame of one 10-bit level reads back as that level's green pair on the 0-255 scale.
func TestLumaGridReadsThePackedFrame(t *testing.T) {
	raw := make([]byte, frameBytes)
	v := uint16(600) // 10 bits
	for i := 0; i+5 <= len(raw); i += 5 {
		// Four pixels of v, least significant bits first.
		raw[i] = byte(v)
		raw[i+1] = byte(v>>8) | byte(v<<2&0xFF)
		raw[i+2] = byte(v>>6) | byte(v<<4&0xFF)
		raw[i+3] = byte(v>>4) | byte(v<<6&0xFF)
		raw[i+4] = byte(v >> 2)
	}
	g := (&Frame{raw: raw}).Luma(32, 24)
	if len(g) != 32*24 {
		t.Fatalf("%d cells", len(g))
	}
	want := uint8(min((2*v)>>3, 255))
	for i, c := range g {
		if c != want {
			t.Fatalf("cell %d = %d, want %d", i, c, want)
		}
	}
}
