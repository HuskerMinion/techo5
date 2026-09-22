package mic

import (
	"math"
	"testing"
)

// A listener keeps the frames it is handed. Its channel is eight deep and it is read from another
// goroutine — a wake engine, or the stream Home Assistant transcribes — so it is routinely a frame or
// two behind the reader. The canceller and the vendor beamformer both work in a buffer they reuse, so
// handing that buffer straight on gave a listener that was behind the newest frame several times over
// and lost the ones it had not reached yet. Nothing noticed: the audio was still audio, and a
// streaming wake model fed the same frame twice simply scores worse.
//
// These drive broadcast with frames that say which one they are, and read them only once they have
// all been sent, which is the listener that is behind.

// reusedBuffer is a mixer that hands back the same buffer every frame, carrying that frame's number
// in every sample. It is what the real producers on this path do, with the content made obvious.
type reusedBuffer struct {
	buf []int16
	nth int16
}

func (m *reusedBuffer) Mix(mics [][]int16) []int16 {
	if len(mics) == 0 || len(mics[0]) == 0 {
		return nil
	}
	if cap(m.buf) < len(mics[0]) {
		m.buf = make([]int16, len(mics[0]))
	}
	m.buf = m.buf[:len(mics[0])]

	m.nth++
	for i := range m.buf {
		m.buf[i] = m.nth
	}
	return m.buf
}

// testSource is the array with the hardware left out, and the denoiser and the leveler off so that
// what arrives is the frame the mixer made rather than what those two made of it.
func testSource() *Source {
	s := New()
	s.cancelling = false
	s.leveling.Store(false)
	s.denoising.Store(false)
	return s
}

// frames the listener's channel holds, so nothing here is dropped.
const behindFrames = 8

func TestListenerBehindKeepsEveryFrame(t *testing.T) {
	s := testSource()
	s.mixer = &reusedBuffer{}

	frames, stop := s.Listen("behind")
	defer stop()

	for i := range behindFrames {
		s.broadcast(rawFrame(i, false))
	}

	for i := range behindFrames {
		select {
		case frame := <-frames:
			if len(frame) != FrameSamples {
				t.Fatalf("frame %d is %d samples, want %d", i, len(frame), FrameSamples)
			}
			want := int16(i + 1)
			for _, v := range frame {
				if v != want {
					t.Fatalf("frame %d carries %d, want %d: the reader wrote over a frame a listener was still holding",
						i, v, want)
				}
			}
		default:
			t.Fatalf("%d frames arrived, want %d", i, behindFrames)
		}
	}
}

// The same, through the echo canceller, which is the path the device runs by default and the one the
// wake word goes missing on while music is playing. What is checked is the buffers rather than the
// samples: cancellation is adaptive, so what comes out of it is not something to predict, but two
// frames that are the same buffer can only be one frame delivered twice.
func TestCancelledFramesAreNotTheSameBuffer(t *testing.T) {
	s := testSource()
	if s.cancel == nil {
		t.Skip("no canceller in this build")
	}
	s.cancelling = true

	frames, stop := s.Listen("behind")
	defer stop()

	for i := range behindFrames {
		s.broadcast(rawFrame(i, true))
	}
	if !s.Cancelling() {
		t.Fatal("the canceller did not engage: the loopback in these frames is too quiet")
	}

	seen := map[*int16]int{}
	for i := range behindFrames {
		select {
		case frame := <-frames:
			if len(frame) == 0 {
				t.Fatalf("frame %d is empty", i)
			}
			if first, ok := seen[&frame[0]]; ok {
				t.Fatalf("frames %d and %d are the same buffer: the reader overwrites what a listener still holds",
					first, i)
			}
			seen[&frame[0]] = i
		default:
			t.Fatalf("%d frames arrived, want %d", i, behindFrames)
		}
	}
}

// rawFrame builds one captured period as the hardware lays it out: every microphone holding a steady
// level that says which frame this is, and the loopback either silent or carrying a tone loud enough
// for the canceller to engage.
func rawFrame(nth int, playing bool) []byte {
	frameBytes := Channels * Bits / 8

	raw := make([]byte, FrameSamples*frameBytes)
	for f := range FrameSamples {
		off := f * frameBytes
		for c := range Mics {
			putS24LE3(raw[off+c*3:], int32(nth+1)<<8)
		}
		if !playing {
			continue
		}
		// One continuous tone across the frames, so the reference is not the same block every time.
		at := float64(nth*FrameSamples+f) / Rate
		v := int32(8000 * math.Sin(2*math.Pi*440*at))
		for c := range Refs {
			putS24LE3(raw[off+(RefFirst+c)*3:], v<<8)
		}
	}
	return raw
}

// putS24LE3 writes one sample in the capture format: three bytes, little end first.
func putS24LE3(b []byte, v int32) {
	b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16)
}
