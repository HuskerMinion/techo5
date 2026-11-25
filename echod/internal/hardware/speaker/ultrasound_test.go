package speaker

import (
	"encoding/binary"
	"math"
	"testing"
)

// The tone comes in at the level asked for, on every channel, in a whole number of cycles; leaves
// without a click; and changes frequency only once the old one has faded out.
func TestTheUltrasoundToneFadesInAndOut(t *testing.T) {
	const rate = 96000
	var u ultraTone
	level := float32(0.2)
	buf := make([]byte, rate/10*2*2) // 100 ms, stereo
	peak := func(b []byte, ch int) float64 {
		p := 0.0
		for i := 2 * ch; i+2 <= len(b); i += 4 {
			p = max(p, math.Abs(float64(int16(binary.LittleEndian.Uint16(b[i:])))))
		}
		return p / 32767
	}
	u.mix(buf, 2, rate, 32000, level)
	tail := buf[len(buf)/2:]
	for ch := range 2 {
		if p := peak(tail, ch); math.Abs(p-float64(level)) > 0.01 {
			t.Errorf("channel %d peaks at %.3f, want %.3f", ch, p, level)
		}
	}
	// The first samples are near silence: no step in.
	if p := peak(buf[:16], 0); p > 0.01 {
		t.Errorf("the tone starts at %.3f", p)
	}

	for range 3 {
		clear(buf)
		u.mix(buf, 2, rate, 36000, level)
	}
	if u.hz != 36000 {
		t.Fatalf("the tone is still %d Hz after 300 ms", u.hz)
	}
	for range 3 {
		clear(buf)
		u.mix(buf, 2, rate, 0, level)
	}
	if p := peak(buf, 0); p > 0.001 {
		t.Errorf("stopped, the tone is still at %.4f", p)
	}
}

func TestTheUltrasoundToneSaturates(t *testing.T) {
	var u ultraTone
	buf := make([]byte, 9600*4)
	for i := 0; i < len(buf); i += 2 {
		binary.LittleEndian.PutUint16(buf[i:], uint16(int16(32000)))
	}
	u.mix(buf, 2, 96000, 24000, 0.5)
	for i := 0; i < len(buf); i += 2 {
		if v := int16(binary.LittleEndian.Uint16(buf[i:])); v < 0 {
			t.Fatalf("sample %d wrapped to %d", i/2, v)
		}
	}
}

func TestUltrasoundTables(t *testing.T) {
	for _, c := range []struct {
		hz, rate, n int
	}{{32000, 96000, 3}, {34000, 96000, 48}, {30000, 96000, 16}, {48000, 96000, 0}, {20000, 48000, 12}, {33333, 96000, 0}} {
		if got := len(ultraTable(c.hz, c.rate)); got != c.n {
			t.Errorf("%d Hz at %d: a loop of %d, want %d", c.hz, c.rate, got, c.n)
		}
	}
}
