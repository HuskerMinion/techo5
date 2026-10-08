package media

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/mewkiz/flac"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// FLAC, as a music server on the home network sends a song over DLNA (feature/dlna): decoded frame by
// frame as it arrives, any bit depth brought to 16 bits, mono spread to both sides and more than two
// channels folded into the first two, then brought to the speaker's rate.

// mostFLACChannels and the rate bounds keep a stream that claims something absurd from being taken on.
const (
	mostFLACChannels = 8
	leastFLACRate    = 8000
	mostFLACRate     = 192000
)

type flacSamples struct {
	s   *flac.Stream
	rs  resampler
	out []byte
	err error
}

func newFLACSamples(r io.Reader) (_ *flacSamples, err error) {
	// The decoder panics on some lying streams (feature/sendspin's FLAC says the same); a stream from
	// the network is just a stream that cannot be played.
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("flac: %v", p)
		}
	}()
	s, err := flac.New(r)
	if err != nil {
		return nil, fmt.Errorf("flac: %w", err)
	}
	info := s.Info
	if info == nil || info.NChannels < 1 || info.NChannels > mostFLACChannels ||
		info.SampleRate < leastFLACRate || info.SampleRate > mostFLACRate || info.BitsPerSample < 4 || info.BitsPerSample > 32 {
		return nil, errors.New("flac: a stream this device cannot play")
	}
	return &flacSamples{s: s, rs: resampler{from: int(info.SampleRate), to: speaker.Rate}}, nil
}

func (f *flacSamples) Read(p []byte) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			f.err = fmt.Errorf("flac: %v", r)
			n, err = 0, f.err
		}
	}()
	for len(f.out) == 0 {
		if f.err != nil {
			return 0, f.err
		}
		frame, err := f.s.ParseNext()
		if err != nil {
			f.err = err
			continue
		}
		n := len(frame.Subframes)
		if n == 0 || len(frame.Subframes[0].Samples) == 0 {
			continue
		}
		shift := int(frame.BitsPerSample) - 16
		count := len(frame.Subframes[0].Samples)
		left, right := frame.Subframes[0].Samples, frame.Subframes[0].Samples
		if n > 1 && len(frame.Subframes[1].Samples) == count {
			right = frame.Subframes[1].Samples
		}
		samples := make([]int16, 0, 2*count)
		for i := range count {
			samples = append(samples, to16(left[i], shift), to16(right[i], shift))
		}
		samples = f.rs.run(samples)
		f.out = make([]byte, len(samples)*2)
		for i, s := range samples {
			binary.LittleEndian.PutUint16(f.out[i*2:], uint16(s))
		}
	}
	n = copy(p, f.out)
	f.out = f.out[n:]
	return n, nil
}

// to16 brings a sample of 16+shift bits to 16.
func to16(v int32, shift int) int16 {
	switch {
	case shift > 0:
		v >>= shift
	case shift < 0:
		v <<= -shift
	}
	return int16(max(min(v, 32767), -32768))
}
