//go:build linux

package vpu

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// The host's tables for Fire OS's strongest tuning: the weight starts at 256 × 2^(-7.84/300) and
// falls by e^(-√2/3) a level; the blend share is 0 under the threshold and 64-BlendW from twice it.
func TestSWNRTables(t *testing.T) {
	w := weightTable(NRStrongest)
	if w[0] != 251 || w[1] != 157 || w[2] != 98 || w[30] != 0 {
		t.Fatalf("weights %v", w[:8])
	}
	for i := 1; i < len(w); i++ {
		if w[i] > w[i-1] {
			t.Fatalf("weights rise at %d", i)
		}
	}
	b := blendTable(NRStrongest)
	if b[0] != 0 || b[4] != 0 || b[5] != 0 || b[9] == 0 || b[9] >= 32 || b[10] != 32 || b[511] != 32 {
		t.Fatalf("blend %v", b[:12])
	}
	if b := blendTable(NRParams{K: 1, Strength: 1, BlendW: 64}); b != [blendLen]byte{} {
		t.Fatal("a blend weight of 64 keeps the plane as it came, and needs no table")
	}
}

func TestInterleave(t *testing.T) {
	const w, h, stride = 13, 3, 32
	uv := make([]byte, stride*h)
	for i := range uv {
		uv[i] = byte(i)
	}
	u, v := make([]byte, w*h), make([]byte, w*h)
	Deinterleave(u, v, uv, w, h, stride)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if u[y*w+x] != byte(y*stride+2*x) || v[y*w+x] != byte(y*stride+2*x+1) {
				t.Fatalf("at %d,%d: u %d v %d", x, y, u[y*w+x], v[y*w+x])
			}
		}
	}
	if u[0] != 0 || v[0] != 1 || u[w] != stride || v[2*w+5] != 2*stride+11 {
		t.Fatalf("u %v v %v", u, v)
	}
	back := make([]byte, len(uv))
	copy(back, uv)
	for y := 0; y < h; y++ {
		clear(back[y*stride : y*stride+2*w])
	}
	Interleave(back, u, v, w, h, stride)
	if !bytes.Equal(back, uv) {
		t.Fatal("not its own inverse, or the padding was touched")
	}
}

// The work buffer's layout as the host lays it out (VPUIIRCore::VPUInit) for 1456x1088, and the
// planes on 4 KiB boundaries (the DSP mangles a plane that is not).
func TestSWNRLayout(t *testing.T) {
	d := &Device{data: make([]byte, 5<<20), high: 5 << 20}
	c := &Chroma{d: d, w: 728, h: 544}
	c.base = (uint32(len(d.data)) - c.size()) &^ 0xffff
	pl := uint32(728 * 544)
	if c.uOff()%4096 != 0 || c.vOff()%4096 != 0 || c.workOff()%4096 != 0 || c.workOff()+c.workLen() > uint32(len(d.data)) {
		t.Fatalf("u %#x v %#x work %#x+%#x", c.uOff(), c.vOff(), c.workOff(), c.workLen())
	}
	le := binary.LittleEndian
	s := c.settings(stagePlane, 1)
	want := []uint32{728, 544, 728, 5, 1, 0, 0, 2*pl + 0x200, 3*pl + 0x300, pl, 5*pl + 0x300, 6*pl + 0x300 + 4*728, 0, pl, 2 * pl}
	for i, x := range want {
		if got := le.Uint32(s[4*i:]); got != x {
			t.Fatalf("setting %d: %#x, not %#x", i, got, x)
		}
	}
	if c.workLen() != 728*(6*544+8)+0x300 {
		t.Fatal("work buffer")
	}
}

// The data area's two ends: a net placed from the bottom and swnr's pictures from the top must not meet.
func TestReserve(t *testing.T) {
	d := &Device{data: make([]byte, 5<<20), high: 5 << 20}
	if err := d.reserve(1<<20, false); err != nil {
		t.Fatal(err)
	}
	if err := d.reserve(1<<19, true); err == nil {
		t.Fatal("the top taken over the bottom's part")
	}
	if err := d.reserve(2<<20, true); err != nil {
		t.Fatal(err)
	}
	if err := d.reserve(3<<20, false); err == nil {
		t.Fatal("the bottom taken over the top's part")
	}
}
