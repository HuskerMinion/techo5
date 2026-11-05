package mic

import (
	"bytes"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/asp"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

func TestPCMOnlyValidPlainS16AndS32(t *testing.T) {
	cases := []struct {
		format int
		bits   int
		codec  uint8
	}{
		{alsa.FormatS16_LE, 16, asp.FormatS16},
		{alsa.FormatS32_LE, 32, asp.FormatS32},
	}
	for _, tc := range cases {
		cfg := alsa.Config{Channels: Mics, Rate: 32000, Format: tc.format, Bits: tc.bits}
		src := bytes.Repeat([]byte{0xb7}, 2*Mics*(tc.bits/8))
		n, err := plainNative(src, cfg)
		if err != nil || n.Geometry.Buffers[0].Format != tc.codec || len(n.Geometry.Channels) != Mics {
			t.Fatalf("format %d: %v %+v", tc.format, err, n.Geometry)
		}
		if _, err := asp.Encode(n.Descriptor()); err != nil {
			t.Fatal(err)
		}
	}
}
