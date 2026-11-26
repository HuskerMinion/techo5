package addonapi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// WAV is a sound from an add-on's own files, as sound.play takes one: mono 16-bit PCM, at most MaxWAV
// bytes and MaxWAVLength long.
type WAV struct {
	Rate    int
	Samples []int16
}

// Length is how long it plays.
func (w WAV) Length() time.Duration {
	if w.Rate <= 0 {
		return 0
	}
	return time.Duration(len(w.Samples)) * time.Second / time.Duration(w.Rate)
}

// ParseWAV reads a WAVE file for sound.play, walking its chunks rather than assuming a 44-byte header,
// and refuses anything but what the API promises to play. The daemon checks again before it plays one;
// an author's tests can check a file with this before it is pinned.
func ParseWAV(b []byte) (WAV, error) {
	var w WAV
	if len(b) > MaxWAV {
		return w, fmt.Errorf("the sound is %d bytes, more than %d", len(b), MaxWAV)
	}
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return w, errors.New("the sound is not a WAVE file")
	}
	var format, channels, bits int
	sawFmt := false
	for off := 12; off+8 <= len(b); {
		id := string(b[off : off+4])
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		off += 8
		// A size past the end, or one that would overflow, is cut to what is there: a chunk is never
		// read past the file.
		end := len(b)
		if size >= 0 && size <= len(b)-off {
			end = off + size
		}
		switch id {
		case "fmt ":
			if end-off < 16 {
				return w, errors.New("the sound's fmt chunk is short")
			}
			format = int(binary.LittleEndian.Uint16(b[off:]))
			channels = int(binary.LittleEndian.Uint16(b[off+2:]))
			w.Rate = int(binary.LittleEndian.Uint32(b[off+4:]))
			bits = int(binary.LittleEndian.Uint16(b[off+14:]))
			sawFmt = true
		case "data":
			switch {
			case !sawFmt:
				return w, errors.New("the sound has no fmt chunk before its data")
			case format != 1:
				return w, fmt.Errorf("the sound is in format %#x, not plain PCM", format)
			case channels != 1:
				return w, fmt.Errorf("the sound has %d channels; it has to be mono", channels)
			case bits != 16:
				return w, fmt.Errorf("the sound is %d-bit; it has to be 16-bit", bits)
			case w.Rate < 8000 || w.Rate > 48000:
				return w, fmt.Errorf("the sound is at %d Hz, outside 8000 to 48000", w.Rate)
			}
			n := (end - off) / 2
			w.Samples = make([]int16, n)
			for i := range n {
				w.Samples[i] = int16(binary.LittleEndian.Uint16(b[off+2*i:]))
			}
			if len(w.Samples) == 0 {
				return w, errors.New("the sound holds nothing")
			}
			if w.Length() > MaxWAVLength {
				return w, fmt.Errorf("the sound is %v long, more than %v", w.Length().Round(time.Millisecond), MaxWAVLength)
			}
			return w, nil
		}
		// Chunks are padded to an even length.
		off = end + (end-off)&1
		if end == len(b) {
			break
		}
	}
	return w, errors.New("the sound has no data chunk")
}
