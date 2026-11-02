// Package asp encodes the External ASP v1 descriptor without adapting PCM.
package asp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	Magic                   = "ASP1"
	Version                 = 1
	HeaderSize              = 16
	BufferSize              = 28
	ChannelSize             = 8
	MaxDescriptorSize       = HeaderSize + 255*BufferSize + 255*ChannelSize
	ReadyByte         byte  = 0xa5
	OutputBytes             = 640
	FormatS16         uint8 = 1
	FormatS24_3       uint8 = 2
	FormatS32         uint8 = 3
	RoleMIC           uint8 = 1
	RoleREF           uint8 = 2
)

type Buffer struct {
	Offset, Length, SampleRate, Frames, Stride uint32
	Format, ValidBits, LSBShift, Channels      uint8
	FirstChannel                               uint16
}
type Channel struct {
	BufferIndex, Role uint8
	RoleIndex         uint16
	FrameOffset       uint32
}

// Descriptor contains only physical PCM buffer and captured channel geometry.
type Descriptor struct {
	Buffers  []Buffer
	Channels []Channel
}

// Encode writes the PCM-only v1 wire bytes with exact 16+28*N+8*M length.
func Encode(d Descriptor) ([]byte, error) {
	nb, nc := len(d.Buffers), len(d.Channels)
	if nb < 1 || nb > 255 || nc < 1 || nc > 255 {
		return nil, errors.New("invalid PCM buffer/channel counts")
	}
	sz := HeaderSize + nb*BufferSize + nc*ChannelSize
	if sz > MaxDescriptorSize {
		return nil, errors.New("descriptor exceeds PCM-only maximum")
	}
	var seenMic, seenRef [255]bool
	micCount, refCount := 0, 0
	var cumulative, first uint64
	for i, b := range d.Buffers {
		var width uint64
		switch b.Format {
		case FormatS16:
			width = 2
		case FormatS24_3:
			width = 3
		case FormatS32:
			width = 4
		default:
			return nil, fmt.Errorf("buffer %d bad format", i)
		}
		if b.SampleRate == 0 || b.Frames == 0 || b.Stride == 0 || b.Length == 0 || b.Channels == 0 ||
			uint64(b.Offset) != cumulative || uint64(b.FirstChannel) != first ||
			first+uint64(b.Channels) > uint64(nc) || b.ValidBits < 2 || uint64(b.ValidBits) > width*8 ||
			uint64(b.ValidBits)+uint64(b.LSBShift) > width*8 {
			return nil, fmt.Errorf("buffer %d bad geometry", i)
		}
		for c := first; c < first+uint64(b.Channels); c++ {
			ch := d.Channels[c]
			end := uint64(ch.FrameOffset) + width
			if int(ch.BufferIndex) != i || end > uint64(b.Stride) || (uint64(b.Frames)-1)*uint64(b.Stride)+end > uint64(b.Length) {
				return nil, fmt.Errorf("channel %d bounds", c)
			}
			for o := first; o < c; o++ {
				prev := uint64(d.Channels[o].FrameOffset)
				if uint64(ch.FrameOffset) < prev+width && prev < end {
					return nil, errors.New("overlapping samples")
				}
			}
			if ch.RoleIndex >= 255 {
				return nil, errors.New("role index overflow")
			}
			switch ch.Role {
			case RoleMIC:
				if seenMic[ch.RoleIndex] {
					return nil, errors.New("duplicate MIC index")
				}
				seenMic[ch.RoleIndex] = true
				micCount++
			case RoleREF:
				if seenRef[ch.RoleIndex] {
					return nil, errors.New("duplicate REF index")
				}
				seenRef[ch.RoleIndex] = true
				refCount++
			default:
				return nil, errors.New("invalid role")
			}
		}
		cumulative += uint64(b.Length)
		if cumulative > uint64(^uint32(0)) {
			return nil, errors.New("input size overflow")
		}
		first += uint64(b.Channels)
	}
	if first != uint64(nc) || micCount == 0 {
		return nil, errors.New("coverage/MIC")
	}
	for i := 0; i < micCount; i++ {
		if !seenMic[i] {
			return nil, errors.New("noncontiguous MIC")
		}
	}
	for i := 0; i < refCount; i++ {
		if !seenRef[i] {
			return nil, errors.New("noncontiguous REF")
		}
	}
	out := make([]byte, sz)
	copy(out, Magic)
	binary.LittleEndian.PutUint16(out[4:], Version)
	out[6], out[7] = byte(nb), byte(nc)
	binary.LittleEndian.PutUint32(out[8:], uint32(sz))
	binary.LittleEndian.PutUint32(out[12:], uint32(cumulative))
	p := HeaderSize
	for _, b := range d.Buffers {
		binary.LittleEndian.PutUint32(out[p:], b.Offset)
		binary.LittleEndian.PutUint32(out[p+4:], b.Length)
		binary.LittleEndian.PutUint32(out[p+8:], b.SampleRate)
		binary.LittleEndian.PutUint32(out[p+12:], b.Frames)
		binary.LittleEndian.PutUint32(out[p+16:], b.Stride)
		out[p+20], out[p+21], out[p+22], out[p+23] = b.Format, b.ValidBits, b.LSBShift, b.Channels
		binary.LittleEndian.PutUint16(out[p+24:], b.FirstChannel)
		p += BufferSize
	}
	for _, c := range d.Channels {
		out[p], out[p+1] = c.BufferIndex, c.Role
		binary.LittleEndian.PutUint16(out[p+2:], c.RoleIndex)
		binary.LittleEndian.PutUint32(out[p+4:], c.FrameOffset)
		p += ChannelSize
	}
	return out, nil
}
