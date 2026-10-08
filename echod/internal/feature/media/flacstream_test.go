package media

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/mewkiz/flac"
	flacframe "github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// encodeFLAC makes a FLAC stream of n frames per channel: a left and right ramp at the given rate and depth.
func encodeFLAC(t *testing.T, rate uint32, bits uint8, channels, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	info := &meta.StreamInfo{BlockSizeMin: 1024, BlockSizeMax: 1024, SampleRate: rate, NChannels: uint8(channels),
		BitsPerSample: bits, NSamples: uint64(n)}
	enc, err := flac.NewEncoder(&buf, info)
	if err != nil {
		t.Fatal(err)
	}
	layout := flacframe.ChannelsMono
	if channels == 2 {
		layout = flacframe.ChannelsLR
	}
	for off := 0; off < n; off += 1024 {
		size := min(1024, n-off)
		f := &flacframe.Frame{Header: flacframe.Header{HasFixedBlockSize: true, BlockSize: uint16(size), SampleRate: rate,
			Channels: layout, BitsPerSample: bits}}
		for ch := range channels {
			s := make([]int32, size)
			for i := range s {
				v := int32((off + i) % 100)
				if ch == 1 {
					v = -v
				}
				s[i] = v << (bits - 16)
			}
			f.Subframes = append(f.Subframes, &flacframe.Subframe{SubHeader: flacframe.SubHeader{Pred: flacframe.PredVerbatim}, Samples: s, NSamples: size})
		}
		if err := enc.WriteFrame(f); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A FLAC stream is told by its first bytes and comes out as the speaker's own samples: 24-bit brought to
// 16, both sides kept apart, and a mono one on both.
func TestAFLACStreamIsDecoded(t *testing.T) {
	for _, c := range []struct {
		name     string
		bits     uint8
		channels int
	}{{"24-bit stereo", 24, 2}, {"16-bit mono", 16, 1}} {
		t.Run(c.name, func(t *testing.T) {
			data := encodeFLAC(t, uint32(speaker.Rate), c.bits, c.channels, 4096)
			src, err := DecodeStream(bufio.NewReader(bytes.NewReader(data)), "")
			if err != nil {
				t.Fatal(err)
			}
			pcm, err := io.ReadAll(src)
			if err != nil && err != io.EOF {
				t.Fatal(err)
			}
			if len(pcm) != 4096*4 {
				t.Fatalf("got %d bytes, want %d", len(pcm), 4096*4)
			}
			for _, i := range []int{1, 57, 4000} {
				l := int16(binary.LittleEndian.Uint16(pcm[i*4:]))
				r := int16(binary.LittleEndian.Uint16(pcm[i*4+2:]))
				want := int16(i % 100)
				wantR := -want
				if c.channels == 1 {
					wantR = want
				}
				if l != want || r != wantR {
					t.Errorf("frame %d = %d,%d, want %d,%d", i, l, r, want, wantR)
				}
			}
		})
	}
}

// A FLAC stream at 44.1 kHz comes out at the speaker's rate.
func TestAFLACStreamIsBroughtToTheSpeakersRate(t *testing.T) {
	data := encodeFLAC(t, 44100, 16, 2, 44100)
	src, err := DecodeStream(bufio.NewReader(bytes.NewReader(data)), "audio/flac")
	if err != nil {
		t.Fatal(err)
	}
	pcm, _ := io.ReadAll(src)
	frames := len(pcm) / 4
	if frames < speaker.Rate-200 || frames > speaker.Rate+200 {
		t.Errorf("one second came out as %d frames, want about %d", frames, speaker.Rate)
	}
}
