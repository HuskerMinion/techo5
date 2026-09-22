package sendspin

import (
	"bytes"
	"testing"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

const flacBlock = 1024

// flacStream encodes blocks of a known signal, and reports the samples that went in so what comes out
// can be held against them.
func flacStream(t *testing.T, blocks int) (encoded []byte, want []int16) {
	t.Helper()

	var buf bytes.Buffer
	info := &meta.StreamInfo{
		BlockSizeMin:  flacBlock,
		BlockSizeMax:  flacBlock,
		SampleRate:    speaker.Rate,
		NChannels:     speaker.Channels,
		BitsPerSample: speaker.Bits,
		NSamples:      uint64(flacBlock * blocks),
	}

	enc, err := flac.NewEncoder(&buf, info)
	if err != nil {
		t.Fatalf("flac.NewEncoder: %v", err)
	}

	for b := range blocks {
		subs := make([]*frame.Subframe, speaker.Channels)
		for c := range subs {
			samples := make([]int32, flacBlock)
			for i := range samples {
				// Distinct per channel and per block, so a swap or an off-by-one block is visible.
				samples[i] = int32((b*flacBlock+i)%2000 - 1000 + c*3)
				want = append(want, 0)
			}
			subs[c] = &frame.Subframe{
				SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
				Samples:   samples,
				NSamples:  flacBlock,
			}
		}

		// Interleave into want in the order the decoder must produce.
		at := b * flacBlock * speaker.Channels
		for i := range flacBlock {
			for c := range speaker.Channels {
				want[at+i*speaker.Channels+c] = int16(subs[c].Samples[i])
			}
		}

		f := &frame.Frame{
			Header: frame.Header{
				HasFixedBlockSize: true,
				BlockSize:         flacBlock,
				SampleRate:        speaker.Rate,
				Channels:          frame.ChannelsLR,
				BitsPerSample:     speaker.Bits,
				Num:               uint64(b),
			},
			Subframes: subs,
		}
		if err := enc.WriteFrame(f); err != nil {
			t.Fatalf("WriteFrame %d: %v", b, err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("closing the encoder: %v", err)
	}
	return buf.Bytes(), want
}

// drain feeds the stream in pieces and gathers every frame that comes back, which is what the session
// does with chunks off the wire.
func drain(t *testing.T, d decoder, rest []byte, piece int) []int16 {
	t.Helper()

	var got []int16
	for len(rest) > 0 {
		n := min(piece, len(rest))
		pcm, err := d.decode(rest[:n])
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		got = append(got, pcm...)
		rest = rest[n:]
	}

	// The parser runs alongside, so the last frames can still be in flight after the final chunk.
	for range 200 {
		pcm, err := d.decode(nil)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(pcm) == 0 {
			break
		}
		got = append(got, pcm...)
	}
	return got
}

// The header and the frames arrive separately — the header once in stream/start, the frames as chunks —
// and only together are they a stream mewkiz/flac will read.
func TestFLACDecodesWhatWasEncoded(t *testing.T) {
	encoded, want := flacStream(t, 3)

	// Anywhere in the metadata will do: the decoder concatenates the header with what follows, so the
	// split does not have to land on the real boundary.
	d, err := newFLACDecoder(encoded[:4])
	if err != nil {
		t.Fatalf("newFLACDecoder: %v", err)
	}
	defer d.close()

	got := drain(t, d, encoded[4:], 512)

	if len(got) != len(want) {
		t.Fatalf("decoded %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d is %d, want %d", i, got[i], want[i])
		}
	}
}

// A frame split across chunks yields nothing until the chunk that completes it, which the session has
// to tell apart from silence rather than placing an empty frame.
func TestFLACSaysNothingUntilAFrameIsWhole(t *testing.T) {
	encoded, want := flacStream(t, 1)

	d, err := newFLACDecoder(encoded[:4])
	if err != nil {
		t.Fatalf("newFLACDecoder: %v", err)
	}
	defer d.close()

	// One byte at a time: almost every call must report no frame at all.
	got := drain(t, d, encoded[4:], 1)

	if len(got) != len(want) {
		t.Fatalf("decoded %d samples, want %d", len(got), len(want))
	}
}

func TestFLACRefusesToStartWithoutItsHeader(t *testing.T) {
	if _, err := newFLACDecoder(nil); err == nil {
		t.Error("started with no header")
	}
}

// monoStream encodes a stream that really is one channel, header and frames agreeing.
func monoStream(t *testing.T, blocks int) []byte {
	t.Helper()

	var buf bytes.Buffer
	info := &meta.StreamInfo{
		BlockSizeMin:  flacBlock,
		BlockSizeMax:  flacBlock,
		SampleRate:    speaker.Rate,
		NChannels:     1,
		BitsPerSample: speaker.Bits,
		NSamples:      uint64(flacBlock * blocks),
	}

	enc, err := flac.NewEncoder(&buf, info)
	if err != nil {
		t.Fatalf("flac.NewEncoder: %v", err)
	}
	for b := range blocks {
		samples := make([]int32, flacBlock)
		for i := range samples {
			samples[i] = int32((b*flacBlock+i)%2000 - 1000)
		}
		f := &frame.Frame{
			Header: frame.Header{
				HasFixedBlockSize: true,
				BlockSize:         flacBlock,
				SampleRate:        speaker.Rate,
				Channels:          frame.ChannelsMono,
				BitsPerSample:     speaker.Bits,
				Num:               uint64(b),
			},
			Subframes: []*frame.Subframe{{
				SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
				Samples:   samples,
				NSamples:  flacBlock,
			}},
		}
		if err := enc.WriteFrame(f); err != nil {
			t.Fatalf("WriteFrame %d: %v", b, err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("closing the encoder: %v", err)
	}
	return buf.Bytes()
}

// The sender is on the LAN and never authenticated, so a stream whose header and frames disagree has
// to end the stream rather than the process. STREAMINFO says two channels here and every frame is
// mono: laying the frame out by the header's count indexes a subframe that was never parsed, and the
// parser runs on a goroutine of its own where nothing would have caught it.
func TestFLACRefusesAFrameThatIsNotTheStreamInfo(t *testing.T) {
	encoded := monoStream(t, 2)

	// STREAMINFO's channel count is three bits, one less than the real number, at bit 44 of the block:
	// past "fLaC", the block header, both block sizes, both frame sizes and the 20-bit sample rate.
	// Setting them to 1 makes the header claim the two channels the frames do not have.
	encoded[20] |= 0x02

	d, err := newFLACDecoder(encoded[:4])
	if err != nil {
		t.Fatalf("newFLACDecoder: %v", err)
	}
	defer d.close()

	rest, refused := encoded[4:], false
	for len(rest) > 0 && !refused {
		n := min(512, len(rest))
		if _, err := d.decode(rest[:n]); err != nil {
			refused = true
		}
		rest = rest[n:]
	}
	for i := 0; i < 200 && !refused; i++ {
		if _, err := d.decode(nil); err != nil {
			refused = true
		}
	}
	if !refused {
		t.Error("a stream whose frames are not what its header says was decoded")
	}
}

// Truncated, cut about and padded with rubbish: none of it may take the parser goroutine down, and
// what comes back is either no audio or an error, never a panic.
func TestFLACSurvivesAMangledStream(t *testing.T) {
	encoded, _ := flacStream(t, 2)

	for _, tc := range []struct {
		name   string
		mangle func(b []byte) []byte
	}{
		{"truncated", func(b []byte) []byte { return b[:len(b)/2] }},
		{"header only", func(b []byte) []byte { return b[:8] }},
		{"zeroed body", func(b []byte) []byte {
			out := append([]byte(nil), b...)
			for i := 42; i < len(out); i++ {
				out[i] = 0
			}
			return out
		}},
		{"every other byte flipped", func(b []byte) []byte {
			out := append([]byte(nil), b...)
			for i := 42; i < len(out); i += 2 {
				out[i] ^= 0xFF
			}
			return out
		}},
		{"rubbish appended", func(b []byte) []byte {
			out := append([]byte(nil), b...)
			return append(out, bytes.Repeat([]byte{0xFF}, 4096)...)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broken := tc.mangle(append([]byte(nil), encoded...))
			if len(broken) < 8 {
				t.Fatal("nothing left to feed")
			}
			d, err := newFLACDecoder(broken[:4])
			if err != nil {
				return // refusing the header outright is a refusal too
			}
			defer d.close()

			rest := broken[4:]
			for len(rest) > 0 {
				n := min(256, len(rest))
				if _, err := d.decode(rest[:n]); err != nil {
					return
				}
				rest = rest[n:]
			}
			for range 200 {
				if _, err := d.decode(nil); err != nil {
					return
				}
			}
		})
	}
}
