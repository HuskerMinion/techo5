package mic

import (
	"errors"
	"fmt"

	"github.com/HuskerMinion/techo5/echod/internal/asp"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

// errNativeGeometry identifies an optional External descriptor failure, not
// a capture read failure. Only this error can be downgraded to stock PCM.
var errNativeGeometry = errors.New("mic: native geometry unavailable")

// NativeBlock borrows the capture's original physical buffers until the NEXT capture Read.
// A synchronous consumer must finish before that read. It must not keep these bytes or
// publish them as output. Geometry describes captured channels, not stock DSP layout.
// Buffers are not concatenated, remapped, resampled, scaled or copied.
type NativeBlock struct {
	Buffers  [][]byte
	Geometry asp.Descriptor
}

// nativeInterleaved describes one actual interleaved capture buffer. The caller supplies
// captured roles, never a DSP-specific channel mapping.
func nativeInterleaved(pcm []byte, rate uint32, format, bits, shift uint8, mics, refs int) NativeBlock {
	width := 3
	switch format {
	case asp.FormatS16:
		width = 2
	case asp.FormatS32:
		width = 4
	}
	channels := mics + refs
	stride := channels * width
	n := NativeBlock{
		Buffers: [][]byte{pcm},
		Geometry: asp.Descriptor{
			Buffers: []asp.Buffer{{Length: uint32(len(pcm)), SampleRate: rate,
				Frames: uint32(len(pcm) / stride), Stride: uint32(stride),
				Format: format, ValidBits: bits, LSBShift: shift, Channels: uint8(channels)}},
		},
	}
	for i := 0; i < channels; i++ {
		role, slot := asp.RoleMIC, i
		if i >= mics {
			role, slot = asp.RoleREF, i-mics
		}
		n.Geometry.Channels = append(n.Geometry.Channels, asp.Channel{
			Role: role, RoleIndex: uint16(slot), FrameOffset: uint32(i * width)})
	}
	return n
}

// zeroNative clears only declared samples, using each channel's frame offset, width and
// stride. Padding and guard bytes are never touched. This is the shared SoftwareCut
// operation on native physical buffers, including both hardware REF L and REF R.
func zeroNative(n NativeBlock) {
	for i, b := range n.Geometry.Buffers {
		width := 2
		switch b.Format {
		case asp.FormatS24_3:
			width = 3
		case asp.FormatS32:
			width = 4
		}
		pcm := n.Buffers[i]
		first := int(b.FirstChannel)
		for _, ch := range n.Geometry.Channels[first : first+int(b.Channels)] {
			for f := uint32(0); f < b.Frames; f++ {
				at := uint64(f)*uint64(b.Stride) + uint64(ch.FrameOffset)
				if at+uint64(width) > uint64(len(pcm)) {
					panic("native PCM geometry exceeds capture buffer")
				}
				clear(pcm[int(at) : int(at)+width])
			}
		}
	}
}

// captureReadWithNative is optional: ordinary capture.Read remains unchanged.
// The callback runs synchronously before stock conversion while its input is borrowed.
type captureReadWithNative interface {
	ReadWithNative([]byte, func() bool, func(NativeBlock)) (int, bool, error)
}

// plainNative describes only the capture roles established by the stock device layout.
// A physical buffer with just the declared microphones has no REF. The complete
// known stock layout also includes its original L/R hardware loopback. Unknown
// extra channels are NOT silently labeled REF; nor is an unknown ALSA format
// silently advertised as S24_3LE.
func plainNative(pcm []byte, cfg alsa.Config) (NativeBlock, error) {
	var format uint8
	var width int
	switch cfg.Format {
	case alsa.FormatS16_LE:
		format, width = asp.FormatS16, 2
	case alsa.FormatS24_3LE:
		format, width = asp.FormatS24_3, 3
	case alsa.FormatS32_LE:
		format, width = asp.FormatS32, 4
	default:
		return NativeBlock{}, fmt.Errorf("mic: unsupported native ALSA format %d", cfg.Format)
	}
	if cfg.Bits != width*8 || cfg.Rate <= 0 || cfg.Channels <= 0 {
		return NativeBlock{}, fmt.Errorf("mic: invalid physical capture configuration: %+v", cfg)
	}
	refs := 0
	switch {
	case cfg.Channels == Mics:
		// A MIC-only physical input; do not invent a playback reference.
	case cfg.Channels == Channels && RefFirst == Mics && Channels == Mics+Refs:
		refs = Refs // known original stock hardware loopback slots
	default:
		return NativeBlock{}, fmt.Errorf("mic: unknown capture channel roles: got %d channels, known MIC=%d stock=%d", cfg.Channels, Mics, Channels)
	}
	stride := cfg.Channels * width
	if len(pcm) == 0 || len(pcm)%stride != 0 {
		return NativeBlock{}, fmt.Errorf("mic: incomplete native capture frame: bytes=%d stride=%d", len(pcm), stride)
	}
	return nativeInterleaved(pcm, uint32(cfg.Rate), format, uint8(cfg.Bits), 0, Mics, refs), nil
}

// readCaptured samples the SoftwareCut policy exactly once after capture. Plain
// ALSA returns the original interleaved bytes directly, with no input copies.
// Without a native callback, stock and Panel keep their original Read contract;
// on mute all returned stock bytes are cleared without inventing any geometry.
func readCaptured(pcm capture, raw []byte, policy func() bool, visit func(NativeBlock)) (int, bool, error) {
	if paired, ok := pcm.(captureReadWithNative); ok {
		return paired.ReadWithNative(raw, policy, visit)
	}
	n, err := pcm.Read(raw)
	if err != nil {
		return n, false, err
	}
	cut := policy()
	if visit == nil {
		if cut {
			clear(raw[:n])
		}
		return n, cut, nil
	}
	device, ok := pcm.(interface{ Config() alsa.Config })
	if !ok { // Panel stock socket capture: never claim native hardware REF.
		if cut {
			clear(raw[:n])
		}
		return n, cut, fmt.Errorf("%w: stock-only backend", errNativeGeometry)
	}
	block, err := plainNative(raw[:n], device.Config())
	if err != nil {
		if cut {
			clear(raw[:n])
		} // fail closed even for unsupported geometry
		return n, cut, fmt.Errorf("%w: %v", errNativeGeometry, err)
	}
	if cut {
		zeroNative(block)
	}
	visit(block) // borrowed only until next capture Read
	return n, cut, nil
}

// Descriptor returns only the already captured PCM geometry. Input bytes remain
// borrowed in NativeBlock.Buffers; no metadata or PCM conversion is performed.
func (n NativeBlock) Descriptor() asp.Descriptor {
	return n.Geometry
}
