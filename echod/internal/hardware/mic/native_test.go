package mic

import (
	"bytes"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/asp"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

type fakePlainCapture struct {
	cfg    alsa.Config
	bytes  []byte
	onRead func()
}

func (c *fakePlainCapture) Read(buf []byte) (int, error) {
	if c.onRead != nil {
		c.onRead()
	}
	return copy(buf, c.bytes), nil
}
func (c *fakePlainCapture) Close() error        { return nil }
func (c *fakePlainCapture) Config() alsa.Config { return c.cfg }

func TestPlainBorrowedAndNoREF(t *testing.T) {
	// Fake hardware with no REF, not the stock software-generated REF.
	raw := make([]byte, 2*Mics*3)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	fake := &fakePlainCapture{cfg: alsaConfig(), bytes: raw}
	dst := make([]byte, len(raw))
	visited := 0
	n, _, err := readCaptured(fake, dst, fixedCut(false), func(b NativeBlock) {
		visited++
		if &dst[0] != &b.Buffers[0][0] {
			t.Fatal("plain capture introduced input copy")
		}
		if len(b.Geometry.Buffers) != 1 || b.Geometry.Buffers[0].Frames != 2 ||
			b.Geometry.Buffers[0].SampleRate != 48000 ||
			b.Geometry.Buffers[0].Format != asp.FormatS24_3 ||
			len(b.Geometry.Channels) != Mics || b.Geometry.Channels[len(b.Geometry.Channels)-1].Role != asp.RoleMIC {
			t.Fatalf("wrong plain geometry: %+v", b.Geometry)
		}
		if _, err := asp.Encode(b.Geometry); err != nil {
			t.Fatalf("PCM descriptor: %v", err)
		}
	})
	if err != nil || n != len(raw) || visited != 1 || !bytes.Equal(dst, raw) {
		t.Fatalf("plain read %d/%v/%d %x", n, err, visited, dst)
	}
	_, _, err = readCaptured(fake, dst, fixedCut(true), func(b NativeBlock) {
		if !bytes.Equal(b.Buffers[0], make([]byte, len(raw))) {
			t.Fatal("plain native not cut")
		}
	})
	if err != nil || !bytes.Equal(dst, make([]byte, len(raw))) {
		t.Fatal("plain stock not cut")
	}
}
func alsaConfig() alsa.Config {
	return alsa.Config{Channels: Mics, Rate: 48000, Format: alsa.FormatS24_3LE, Bits: 24}
}

func TestSparseNativeZeroBounds(t *testing.T) {
	// S32 high-aligned MIC and 2 REF channels in a sparse 20-byte stride.
	// Padding bytes and outer canaries must survive the cut unchanged.
	raw := bytes.Repeat([]byte{0x7a}, 43)
	pcm := raw[1:41]
	n := NativeBlock{
		Buffers: [][]byte{pcm},
		Geometry: asp.Descriptor{
			Buffers: []asp.Buffer{{Length: uint32(len(pcm)), Frames: 2, SampleRate: 96000, Stride: 20,
				Format: asp.FormatS32, ValidBits: 24, LSBShift: 8, Channels: 3}},
			Channels: []asp.Channel{
				{Role: asp.RoleMIC, RoleIndex: 0, FrameOffset: 2},
				{Role: asp.RoleREF, RoleIndex: 0, FrameOffset: 8},
				{Role: asp.RoleREF, RoleIndex: 1, FrameOffset: 14},
			},
		},
	}
	if _, err := asp.Encode(n.Geometry); err != nil {
		t.Fatal(err)
	}
	zeroNative(n)
	for i, v := range raw {
		want := byte(0x7a)
		for _, base := range []int{1, 21} {
			for _, off := range []int{2, 8, 14} {
				if i >= base+off && i < base+off+4 {
					want = 0
				}
			}
		}
		if v != want {
			t.Fatalf("byte %d=%02x wanted %02x", i, v, want)
		}
	}
}

func TestOwnedStockRetentionHistoryAndRaw(t *testing.T) {
	s := testSource()
	s.mixer = &reusedBuffer{}
	frames, stop := s.Listen("native-retain")
	defer stop()
	raws, stopRaw := s.ListenRaw()
	defer stopRaw()
	raw := rawFrame(0, false)
	s.broadcastWithCut(raw, false)
	first := <-frames
	original := append([]int16(nil), first...)
	rawFirst := <-raws
	originalRaw := bytes.Clone(rawFirst)
	for i := 1; i <= 5; i++ {
		// Simulate the capture buffer being reused and overwritten.
		copy(raw, rawFrame(i, false))
		s.broadcastWithCut(raw, false)
		<-frames
		<-raws
	}
	for i, v := range first {
		if v != original[i] {
			t.Fatalf("held frame %d mutated", i)
		}
	}
	if !bytes.Equal(rawFirst, originalRaw) {
		t.Fatal("ListenRaw frame mutated")
	}
	recent := s.Recent(20 * time.Millisecond)
	if len(recent) != FrameSamples || recent[0] != 6 {
		t.Fatalf("history incorrect: %v", recent[:min(2, len(recent))])
	}
}

func fixedCut(v bool) func() bool { return func() bool { return v } }

func TestPolicyExactlyOnceAfterPlainRead(t *testing.T) {
	order, calls := 0, 0
	c := &fakePlainCapture{cfg: alsaConfig(), bytes: make([]byte, Mics*3),
		onRead: func() { order = 1 }}
	out := make([]byte, len(c.bytes))
	_, cut, err := readCaptured(c, out, func() bool {
		if order != 1 {
			t.Fatal("SoftwareCut sampled before capture read")
		}
		calls++
		return true
	}, nil)
	if err != nil || !cut || calls != 1 || !bytes.Equal(out, make([]byte, len(out))) {
		t.Fatalf("single capture policy result: cut=%v calls=%d err=%v", cut, calls, err)
	}
}

func TestPlainRejectsUnknownFormatAndChannels(t *testing.T) {
	cases := []struct {
		name string
		cfg  alsa.Config
		n    int
	}{
		{"unknown ALSA format", alsa.Config{Channels: Mics, Rate: 16000, Bits: 24, Format: 999}, Mics * 3},

		{"extra channel after both REF", alsa.Config{Channels: Channels + 1, Rate: 16000, Bits: 24, Format: alsa.FormatS24_3LE}, (Channels + 1) * 3},
		{"format width mismatch", alsa.Config{Channels: Mics, Rate: 16000, Bits: 16, Format: alsa.FormatS24_3LE}, Mics * 3},
		{"incomplete frame", alsa.Config{Channels: Mics, Rate: 16000, Bits: 24, Format: alsa.FormatS24_3LE}, Mics*3 + 1},
	}
	if Refs > 1 {
		cases = append(cases, struct {
			name string
			cfg  alsa.Config
			n    int
		}{
			"unknown channel between MIC and full REF",
			alsa.Config{Channels: Mics + 1, Rate: 16000, Bits: 24, Format: alsa.FormatS24_3LE}, (Mics + 1) * 3,
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := bytes.Repeat([]byte{0x9a}, tc.n)
			fake := &fakePlainCapture{cfg: tc.cfg, bytes: raw}
			dst := make([]byte, tc.n)
			calls := 0
			_, cut, err := readCaptured(fake, dst, func() bool { calls++; return false }, func(NativeBlock) { t.Fatal("invented native descriptor") })
			if err == nil || cut || calls != 1 || !bytes.Equal(dst, raw) {
				t.Fatalf("unsupported input accepted/altered: %v", err)
			}
			// An invalid optional External geometry must not break unmuted
			// built-in capture when no native consumer is present.
			_, cut, err = readCaptured(fake, dst, func() bool { calls++; return false }, nil)
			if err != nil || cut || calls != 2 || !bytes.Equal(dst, raw) {
				t.Fatalf("stock unmuted path changed: %v", err)
			}
			_, cut, err = readCaptured(fake, dst, func() bool { calls++; return true }, func(NativeBlock) { t.Fatal("invented muted descriptor") })
			if err == nil || !cut || calls != 3 || !bytes.Equal(dst, make([]byte, len(dst))) {
				t.Fatalf("unsupported mute did not fail closed: %v", err)
			}
			// Stock Run (no External consumer) must still preserve old Read and
			// the exactly once privacy decision even with an unsupported format.
			_, cut, err = readCaptured(fake, dst, func() bool { calls++; return true }, nil)
			if err != nil || !cut || calls != 4 || !bytes.Equal(dst, make([]byte, len(dst))) {
				t.Fatal("stock privacy regression")
			}
		})
	}
}

func TestPlainKnownStereoREFPreservedAndAbsent(t *testing.T) {
	stereo := make([]byte, Channels*3*2)
	for f := 0; f < 2; f++ {
		at := f * Channels * 3
		for ch := 0; ch < Refs; ch++ {
			copy(stereo[at+(Mics+ch)*3:], []byte{byte(0xa1 + ch), byte(0xb2 + ch), byte(0xc3 + ch)})
		}
	}
	fake := &fakePlainCapture{cfg: alsa.Config{Channels: Channels, Rate: Rate, Bits: 24, Format: alsa.FormatS24_3LE}, bytes: stereo}
	dst := make([]byte, len(stereo))
	n, cut, err := readCaptured(fake, dst, fixedCut(false), func(block NativeBlock) {
		desc := block.Descriptor()
		if len(desc.Channels) != Channels || desc.Channels[Mics].Role != asp.RoleREF ||
			desc.Channels[Mics+Refs-1].Role != asp.RoleREF || desc.Channels[Mics+Refs-1].RoleIndex != uint16(Refs-1) {
			t.Fatalf("roles wrongly derived: %+v", desc.Channels)
		}
		if _, e := asp.Encode(desc); e != nil {
			t.Fatalf("PCM codec rejected REF: %v", e)
		}
		if &block.Buffers[0][0] != &dst[0] {
			t.Fatal("native copy")
		}
		for ch := 0; ch < Refs; ch++ {
			ref := desc.Channels[Mics+ch]
			if ref.Role != asp.RoleREF || ref.RoleIndex != uint16(ch) ||
				ref.FrameOffset != uint32((Mics+ch)*3) {
				t.Fatalf("REF role/slot/index mismatch: %+v", ref)
			}
			for f := 0; f < 2; f++ {
				off := f*Channels*3 + int(ref.FrameOffset)
				want := []byte{byte(0xa1 + ch), byte(0xb2 + ch), byte(0xc3 + ch)}
				if !bytes.Equal(block.Buffers[0][off:off+3], want) {
					t.Fatalf("REF %d frame %d changed: got %x, want %x", ch, f, block.Buffers[0][off:off+3], want)
				}
			}
		}
	})
	if err != nil || cut || n != len(stereo) || !bytes.Equal(stereo, dst) {
		t.Fatal("stereo REF reordered/downmixed")
	}
	noRef := make([]byte, Mics*3)
	block, err := plainNative(noRef, alsaConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range block.Descriptor().Channels {
		if ch.Role == asp.RoleREF {
			t.Fatal("invented REF")
		}
	}
	if _, err := asp.Encode(block.Descriptor()); err != nil {
		t.Fatal(err)
	}
}
